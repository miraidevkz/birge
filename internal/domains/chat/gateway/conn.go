package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/platform/auth"
)

// liveItem is one fan-out notice for a connection, with the message loaded
// and opened once per replica (nil when loading failed).
type liveItem struct {
	notice model.Notice
	opened *model.Opened
}

// ctrlItem is work from the connection's own reader for its writer: a frame
// to send, or a room subscription to start.
type ctrlItem struct {
	frame     any
	subscribe *roomCursor
}

// conn is one client. The reader goroutine (ServeHTTP) parses frames; the
// writer goroutine owns all writes and the per-room sequence state, so frames
// of a room are sent in order and never concurrently.
type conn struct {
	g      *Gateway
	ws     *websocket.Conn
	p      auth.Principal
	ctx    context.Context
	cancel context.CancelFunc
	live   chan liveItem
	ctrl   chan ctrlItem

	closeOnce sync.Once

	mu    sync.Mutex
	rooms map[uuid.UUID]struct{} // registered with the gateway

	seq map[uuid.UUID]int64 // writer goroutine only: last sequence scanned per room
}

func (g *Gateway) run(parent context.Context, ws *websocket.Conn, p auth.Principal) {
	ctx, cancel := context.WithCancel(parent)
	size := g.Options.QueueSize
	if size <= 0 {
		size = DefaultQueueSize
	}
	c := &conn{
		g: g, ws: ws, p: p, ctx: ctx, cancel: cancel,
		live: make(chan liveItem, size), ctrl: make(chan ctrlItem, 16),
		rooms: map[uuid.UUID]struct{}{}, seq: map[uuid.UUID]int64{},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.writeLoop(ctx)
	}()
	// The fan-out callbacks registered from here run on the bus's goroutines
	// with their own bounded contexts, not this connection's.
	c.readLoop() //nolint:contextcheck // see above
	c.kill(websocket.StatusNormalClosure, "")
	<-done
	c.mu.Lock()
	rooms := make([]uuid.UUID, 0, len(c.rooms))
	for id := range c.rooms {
		rooms = append(rooms, id)
	}
	c.mu.Unlock()
	for _, id := range rooms {
		g.remove(id, c)
	}
}

// kill ends the connection once, in the background so a fan-out caller
// never waits for it: the close frame with code goes out first, then both
// goroutines stop. (Cancelling first would abort the pending Read, which
// drops the TCP connection without telling the client why.)
func (c *conn) kill(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		go func() {
			defer c.cancel()
			if err := c.ws.Close(code, reason); err != nil {
				_ = c.ws.CloseNow()
			}
		}()
	})
}

// offer hands a live item to the writer without blocking. A full queue
// means the client does not keep up: it is disconnected and resumes by
// sequence later, so it cannot slow the room down for anyone else.
func (c *conn) offer(it liveItem) {
	if c.ctx.Err() != nil {
		return
	}
	select {
	case c.live <- it:
	default:
		c.g.log().Info("chat slow client disconnected", "event", "ChatSlowConsumer", "room_id", it.notice.RoomID)
		c.kill(CloseSlowConsumer, "slow consumer")
	}
}

func (c *conn) toWriter(it ctrlItem) {
	select {
	case c.ctrl <- it:
	case <-c.ctx.Done():
	}
}

func (c *conn) readLoop() {
	idle := durOr(c.g.Options.IdleTimeout, DefaultIdleTimeout)
	for {
		rctx, cancel := context.WithTimeout(c.ctx, idle)
		typ, data, err := c.ws.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: codeInvalid}})
			continue
		}
		f, err := decode(data)
		if err != nil {
			c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: codeInvalid}})
			continue
		}
		switch f.Type {
		case framePing:
			c.toWriter(ctrlItem{frame: typed{Type: framePong}})
		case frameSubscribe:
			c.subscribe(f.Rooms)
		case frameSend:
			c.send(f)
		}
		if c.ctx.Err() != nil {
			return
		}
	}
}

func (c *conn) subscribe(rooms []roomCursor) {
	if len(rooms) == 0 || len(rooms) > model.MaxSubscribeRooms {
		c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: codeInvalid}})
		return
	}
	for _, r := range rooms {
		if r.RoomID == uuid.Nil || r.AfterSeq < 0 {
			c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: codeInvalid}})
			continue
		}
		if err := c.g.Chat.CanSubscribe(c.ctx, c.p, r.RoomID); err != nil {
			if code := errorCode(err); code != "" {
				c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: code}})
				continue
			}
			c.g.log().Error("chat subscribe failed", "event", "ChatSubscribeFailed", "room_id", r.RoomID, "error", err)
			c.kill(websocket.StatusInternalError, "")
			return
		}
		if err := c.g.add(r.RoomID, c); err != nil {
			c.g.log().Error("chat bus subscribe failed", "event", "ChatSubscribeFailed", "room_id", r.RoomID, "error", err)
			c.kill(websocket.StatusInternalError, "")
			return
		}
		c.mu.Lock()
		c.rooms[r.RoomID] = struct{}{}
		c.mu.Unlock()
		cur := r
		c.toWriter(ctrlItem{subscribe: &cur})
	}
}

func (c *conn) send(f inFrame) {
	if f.RoomID == uuid.Nil {
		c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: codeInvalid}})
		return
	}
	res, err := c.g.Chat.Send(c.ctx, c.p, f.RoomID, f.ClientID, f.Text)
	if res.Crisis != nil {
		// Private to the author, before anything else about the message.
		c.toWriter(ctrlItem{frame: crisisFrameOf(res.Crisis)})
	}
	if err != nil {
		if code := errorCode(err); code != "" {
			c.toWriter(ctrlItem{frame: errorFrame{Type: frameError, Code: code}})
			return
		}
		// Unexpected (database down): close so the client retries the same
		// clientId later, which is idempotent.
		c.g.log().Error("chat send failed", "event", "ChatSendFailed", "room_id", f.RoomID, "error", err)
		c.kill(websocket.StatusInternalError, "")
		return
	}
	c.toWriter(ctrlItem{frame: ackFrame{
		Type: frameAck, ClientID: f.ClientID, MessageID: res.Message.ID, Seq: res.Message.Seq, Status: res.Message.Status,
	}})
}

func (c *conn) writeLoop(ctx context.Context) {
	member := c.p.MemberID
	tick := c.g.Chat.PresenceInterval()
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	defer func() {
		// Presence off, best-effort; a stale row expires by itself.
		octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = c.g.Chat.TouchPresence(octx, member, c.roomIDs(), false)
	}()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case it := <-c.ctrl:
			if it.subscribe != nil {
				c.seq[it.subscribe.RoomID] = it.subscribe.AfterSeq
				if err = c.catchup(ctx, it.subscribe.RoomID); err == nil {
					_ = c.g.Chat.TouchPresence(ctx, member, []uuid.UUID{it.subscribe.RoomID}, true)
				}
			} else {
				err = c.write(ctx, it.frame)
			}
		case it := <-c.live:
			err = c.handleLive(ctx, it)
		case <-ticker.C:
			_ = c.g.Chat.TouchPresence(ctx, member, c.roomIDs(), true)
		}
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, errClosed) {
				c.g.log().Warn("chat connection closed on error", "event", "ChatConnectionError", "error", err)
			}
			c.kill(websocket.StatusInternalError, "")
			return
		}
	}
}

func (c *conn) roomIDs() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(c.seq))
	for id := range c.seq {
		out = append(out, id)
	}
	return out
}

func (c *conn) handleLive(ctx context.Context, it liveItem) error {
	n := it.notice
	last, ok := c.seq[n.RoomID]
	if !ok {
		// Not subscribed (yet): the subscription's catch-up reads the
		// committed message from the database.
		return nil
	}
	switch n.Kind {
	case model.NoticeLeft:
		if n.MemberID != nil && *n.MemberID == c.p.MemberID {
			delete(c.seq, n.RoomID)
			c.mu.Lock()
			delete(c.rooms, n.RoomID)
			c.mu.Unlock()
			c.g.remove(n.RoomID, c)
		}
		return nil
	case model.NoticeNew, model.NoticeChanged:
	default:
		return nil
	}
	if n.Kind == model.NoticeNew && n.Seq <= last {
		return nil // already sent (catch-up got there first)
	}
	if n.Seq > last+1 || it.opened == nil {
		return c.catchup(ctx, n.RoomID) // a gap: fill it from PostgreSQL
	}
	v, show, err := c.g.Chat.Render(ctx, c.p.MemberID, *it.opened)
	if err != nil {
		return err
	}
	if show {
		if err := c.write(ctx, messageFrameOf(v)); err != nil {
			return err
		}
	}
	if n.Kind == model.NoticeNew {
		c.seq[n.RoomID] = n.Seq
	}
	return nil
}

// catchup sends every visible message after the room's last sequence.
func (c *conn) catchup(ctx context.Context, roomID uuid.UUID) error {
	for {
		views, scanned, more, err := c.g.Chat.Catchup(ctx, c.p, roomID, c.seq[roomID], catchupPage)
		if err != nil {
			if errorCode(err) == codeNotMember {
				delete(c.seq, roomID)
				return c.write(ctx, errorFrame{Type: frameError, Code: codeNotMember})
			}
			return err
		}
		for _, v := range views {
			if err := c.write(ctx, messageFrameOf(v)); err != nil {
				return err
			}
		}
		c.seq[roomID] = scanned
		if !more {
			return nil
		}
	}
}

func (c *conn) write(ctx context.Context, frame any) error {
	b, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	wctx, cancel := context.WithTimeout(ctx, durOr(c.g.Options.WriteTimeout, DefaultWriteTimeout))
	defer cancel()
	if err := c.ws.Write(wctx, websocket.MessageText, b); err != nil {
		if ctx.Err() != nil {
			return errClosed
		}
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}
