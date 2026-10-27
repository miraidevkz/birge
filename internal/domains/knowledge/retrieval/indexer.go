package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"birge/internal/domains/knowledge/model"
	"birge/internal/platform/outbox"
)

const (
	indexerConsumerName = "knowledge-retrieval"
	// pointNamespace seeds deterministic point ids from version + chunk index
	// so re-publish of the same version is an upsert, not a duplicate.
	pointNamespace = "birge-knowledge-retrieval-v1"
)

// Article is one published version ready to chunk and embed.
type Article struct {
	ArticleID uuid.UUID
	VersionID uuid.UUID
	Lang      string
	Kind      string
	Title     string
	SourceURL string
	Body      string
}

// Articles loads published versions for indexing and reindex.
type Articles interface {
	// GetPublished returns the version when it is still published and
	// current for retrieval (non-expired rights); model.ErrNotFound otherwise.
	GetPublished(ctx context.Context, versionID uuid.UUID) (Article, error)
	// ListPublished returns every current published fragment.
	ListPublished(ctx context.Context) ([]Article, error)
}

// Embedder embeds queries and passages (TEIEmbedder satisfies this).
type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedPassages(ctx context.Context, texts []string) ([][]float32, error)
}

// Index stores and searches vectors (QdrantIndex satisfies this).
type Index interface {
	Upsert(ctx context.Context, points []Point) error
	DeleteByVersion(ctx context.Context, versionID uuid.UUID) error
	Search(ctx context.Context, vector []float32, lang string, kinds []string, limit int, threshold float32) ([]SearchHit, error)
}

// Indexer consumes knowledge.ArticlePublished / ArticleExpired and rebuilds
// the index from Articles.ListPublished.
type Indexer struct {
	JS       jetstream.JetStream
	Stream   string
	Articles Articles
	Embedder Embedder
	Index    Index
	Logger   *slog.Logger
}

func (i *Indexer) log() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.Default()
}

// EnsureConsumer creates the durable JetStream consumer.
func (i *Indexer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	stream := i.Stream
	if stream == "" {
		stream = "EVENTS"
	}
	cons, err := i.JS.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable: indexerConsumerName,
		FilterSubjects: []string{
			outbox.Subject(model.EventArticlePublished),
			outbox.Subject(model.EventArticleExpired),
		},
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    60 * time.Second,
		MaxDeliver: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("retrieval: create consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one outbox envelope. Safe under redelivery.
func (i *Indexer) HandleMessage(ctx context.Context, msg jetstream.Msg) error {
	var env outbox.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		return fmt.Errorf("retrieval: unmarshal: %w", err)
	}
	switch env.Type {
	case model.EventArticlePublished:
		var data model.ArticlePublishedData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return fmt.Errorf("retrieval: parse ArticlePublished: %w", err)
		}
		return i.IndexVersion(ctx, data.VersionID)
	case model.EventArticleExpired:
		var data model.ArticleExpiredData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return fmt.Errorf("retrieval: parse ArticleExpired: %w", err)
		}
		if err := i.Index.DeleteByVersion(ctx, data.VersionID); err != nil {
			return err
		}
		i.log().Info("retrieval deleted expired", "event", "RetrievalDeleted", "version_id", data.VersionID, "article_id", data.ArticleID)
		return nil
	default:
		i.log().Info("retrieval ignoring event", "event", env.Type, "event_id", env.ID)
		return nil
	}
}

// Run consumes until ctx is cancelled.
func (i *Indexer) Run(ctx context.Context) error {
	cons, err := i.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := i.HandleMessage(ctx, msg); err != nil {
			i.log().Error("retrieval handler", "error", err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("retrieval: consume: %w", err)
	}
	defer cc.Stop()
	<-ctx.Done()
	return nil
}

// IndexVersion loads, chunks, embeds and upserts one published version.
// Missing or no-longer-current versions delete any leftover points.
func (i *Indexer) IndexVersion(ctx context.Context, versionID uuid.UUID) error {
	art, err := i.Articles.GetPublished(ctx, versionID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return i.Index.DeleteByVersion(ctx, versionID)
		}
		return err
	}
	return i.upsertArticle(ctx, art)
}

// Reindex deletes nothing explicitly: it upserts every current published
// version. Callers that change the embedding model use a new collection
// (see package doc) so stale points of the old model are irrelevant.
func (i *Indexer) Reindex(ctx context.Context) (int, error) {
	arts, err := i.Articles.ListPublished(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, art := range arts {
		if err := i.upsertArticle(ctx, art); err != nil {
			return n, err
		}
		n++
	}
	i.log().Info("retrieval reindex done", "event", "RetrievalReindexed", "versions", n)
	return n, nil
}

func (i *Indexer) upsertArticle(ctx context.Context, art Article) error {
	chunks := Chunk(art.Body)
	if len(chunks) == 0 {
		// Empty body: ensure no leftover points from a previous body.
		return i.Index.DeleteByVersion(ctx, art.VersionID)
	}
	vecs, err := i.Embedder.EmbedPassages(ctx, chunks)
	if err != nil {
		return err
	}
	ns := uuid.NewSHA1(uuid.NameSpaceOID, []byte(pointNamespace))
	points := make([]Point, 0, len(chunks))
	for idx, text := range chunks {
		points = append(points, Point{
			ID:     uuid.NewSHA1(ns, []byte(art.VersionID.String()+"/"+strconv.Itoa(idx))),
			Vector: vecs[idx],
			Payload: PointPayload{
				ArticleID: art.ArticleID,
				VersionID: art.VersionID,
				Lang:      art.Lang,
				Kind:      art.Kind,
				Title:     art.Title,
				SourceURL: art.SourceURL,
				Text:      text,
			},
		})
	}
	if err := i.Index.Upsert(ctx, points); err != nil {
		return err
	}
	i.log().Info("retrieval indexed", "event", "RetrievalIndexed", "version_id", art.VersionID, "article_id", art.ArticleID, "chunks", len(points))
	return nil
}
