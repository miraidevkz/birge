// Package gateway is the chat WebSocket endpoint GET /v1/chat/ws
// (contracts/realtime/chat.v1.md, ADR-0016).
//
// # Statelessness
//
// A gateway replica keeps only its own connections. Room fan-out comes
// from NATS core (port.Bus, subject birge.chat.room.<id>, one subscription
// per room with a local subscriber) and every message is read from
// PostgreSQL, so any replica can serve any client and a lost notice costs
// nothing: a connection tracks the last sequence it sent per room and,
// when a notice skips ahead or the client resubscribes after a reconnect,
// fills the gap from the database. Delivery is at-least-once; clients
// deduplicate by message id.
//
// # Per-reader rendering
//
// A notice carries IDs only. The replica loads and opens the message once
// and renders it for each connection: held messages only for their author,
// nothing from a member blocked either way (lookups fail closed; an error
// closes the connection so the client resumes rather than missing a message).
//
// # Backpressure
//
// Each connection has one writer goroutine and a bounded queue of live
// items. The fan-out never blocks on a connection: when the queue is full,
// the client is disconnected (close 1013, "slow consumer") and resumes by
// sequence when it reconnects. Every write has a timeout, so a client that
// stops reading cannot hold its writer forever either.
//
// # Authentication
//
// The member session token is the first frame ({"type":"auth","token":…}),
// never the URL; an invalid token, a non-member principal or no auth frame
// within AuthTimeout closes with 4401. Message text is never logged.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/platform/auth"
)

// Close codes.
const (
	// CloseUnauthorized is sent when the auth frame is missing or invalid (contract).
	CloseUnauthorized websocket.StatusCode = 4401
	// CloseSlowConsumer is sent when a client's queue overflows.
	CloseSlowConsumer = websocket.StatusTryAgainLater
)

// Defaults of Options.
const (
	DefaultQueueSize    = 256
	DefaultAuthTimeout  = 10 * time.Second
	DefaultIdleTimeout  = 90 * time.Second
	DefaultWriteTimeout = 10 * time.Second
	readLimit           = 16 << 10
	catchupPage         = model.PageMax
)

// Chat is what the gateway needs from the chat service (*service.Service).
type Chat interface {
	Send(ctx context.Context, p auth.Principal, roomID, clientID uuid.UUID, text string) (model.SendResult, error)
	CanSubscribe(ctx context.Context, p auth.Principal, roomID uuid.UUID) error
	Catchup(ctx context.Context, p auth.Principal, roomID uuid.UUID, after int64, limit int) ([]model.MessageView, int64, bool, error)
	LoadOpened(ctx context.Context, messageID uuid.UUID) (model.Opened, error)
	Render(ctx context.Context, reader uuid.UUID, o model.Opened) (model.MessageView, bool, error)
	TouchPresence(ctx context.Context, memberID uuid.UUID, roomIDs []uuid.UUID, connected bool) error
	PresenceInterval() time.Duration
}

// Options tune the gateway. Zero values take the defaults.
type Options struct {
	// QueueSize bounds each connection's queue of live items.
	QueueSize    int
	AuthTimeout  time.Duration
	IdleTimeout  time.Duration
	WriteTimeout time.Duration
	// OriginPatterns lists the web origins allowed to connect (coder/websocket
	// AcceptOptions.OriginPatterns). Native apps send no Origin header.
	OriginPatterns []string
}

// Gateway serves GET /v1/chat/ws.
type Gateway struct {
	Chat    Chat
	Auth    port.Authenticator
	Bus     port.Bus
	Logger  *slog.Logger
	Options Options

	mu    sync.Mutex
	rooms map[uuid.UUID]*roomSubs
}

type roomSubs struct {
	conns map[*conn]struct{}
	unsub func()
}

func (g *Gateway) log() *slog.Logger {
	if g.Logger != nil {
		return g.Logger
	}
	return slog.Default()
}

func durOr(v, d time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return d
}

// ServeHTTP upgrades the request and runs the connection until it closes.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  g.Options.OriginPatterns,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return // Accept has written the HTTP error
	}
	ws.SetReadLimit(readLimit)
	ctx := r.Context()
	p, ok := g.authenticate(ctx, ws)
	if !ok {
		_ = ws.Close(CloseUnauthorized, "unauthorized")
		return
	}
	g.run(ctx, ws, p)
}

func (g *Gateway) authenticate(ctx context.Context, ws *websocket.Conn) (auth.Principal, bool) {
	actx, cancel := context.WithTimeout(ctx, durOr(g.Options.AuthTimeout, DefaultAuthTimeout))
	defer cancel()
	typ, data, err := ws.Read(actx)
	if err != nil || typ != websocket.MessageText {
		return auth.Principal{}, false
	}
	f, err := decode(data)
	if err != nil || f.Type != frameAuth || f.Token == "" || g.Auth == nil {
		return auth.Principal{}, false
	}
	p, err := g.Auth.Authenticate(actx, f.Token)
	if err != nil || p.Staff || p.MemberID == uuid.Nil {
		return auth.Principal{}, false
	}
	return p, true
}

// add registers c for the room's live items, subscribing the replica to the
// room's subject on the first local subscriber.
func (g *Gateway) add(roomID uuid.UUID, c *conn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rooms == nil {
		g.rooms = map[uuid.UUID]*roomSubs{}
	}
	rs, ok := g.rooms[roomID]
	if !ok {
		unsub, err := g.Bus.Subscribe(roomID, func(n model.Notice) { g.onNotice(roomID, n) })
		if err != nil {
			return err
		}
		rs = &roomSubs{conns: map[*conn]struct{}{}, unsub: unsub}
		g.rooms[roomID] = rs
	}
	rs.conns[c] = struct{}{}
	return nil
}

// remove unregisters c from the room; the last one unsubscribes the replica.
func (g *Gateway) remove(roomID uuid.UUID, c *conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rs, ok := g.rooms[roomID]
	if !ok {
		return
	}
	delete(rs.conns, c)
	if len(rs.conns) == 0 {
		rs.unsub()
		delete(g.rooms, roomID)
	}
}

func (g *Gateway) conns(roomID uuid.UUID) []*conn {
	g.mu.Lock()
	defer g.mu.Unlock()
	rs, ok := g.rooms[roomID]
	if !ok {
		return nil
	}
	out := make([]*conn, 0, len(rs.conns))
	for c := range rs.conns {
		out = append(out, c)
	}
	return out
}

// Subscribers returns how many local connections receive the room (tests, metrics).
func (g *Gateway) Subscribers(roomID uuid.UUID) int { return len(g.conns(roomID)) }

// onNotice runs on the bus's delivery goroutine for the room. It loads the
// message once and offers it to every local connection without blocking.
func (g *Gateway) onNotice(roomID uuid.UUID, n model.Notice) {
	targets := g.conns(roomID)
	if len(targets) == 0 {
		return
	}
	it := liveItem{notice: n}
	if (n.Kind == model.NoticeNew || n.Kind == model.NoticeChanged) && n.MessageID != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		o, err := g.Chat.LoadOpened(ctx, *n.MessageID)
		cancel()
		if err != nil {
			// Connections still learn that the room moved on and catch up
			// from the database themselves.
			g.log().Warn("chat notice load failed", "event", "ChatNoticeLoadFailed", "room_id", roomID, "error", err)
		} else {
			it.opened = &o
		}
	}
	for _, c := range targets {
		c.offer(it)
	}
}

var errClosed = errors.New("gateway: connection closed")
