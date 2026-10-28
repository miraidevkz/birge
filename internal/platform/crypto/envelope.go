package crypto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"

	"birge/internal/platform/clock"
	"birge/internal/platform/store"
)

// Default DEK cache bounds; see the package documentation for the trade-off.
const (
	DefaultCacheTTL     = 5 * time.Minute
	DefaultCacheEntries = 10_000
)

// Options tune an Envelope. Zero values take the defaults.
type Options struct {
	CacheTTL     time.Duration
	CacheEntries int
	Clock        clock.Clock
}

// Envelope implements Sealer and Shredder on platform.member_keys.
type Envelope struct {
	q     *store.Queries
	keys  *Keyring
	cache *dekCache
	clock clock.Clock
}

var (
	_ Sealer   = (*Envelope)(nil)
	_ Shredder = (*Envelope)(nil)
)

// NewEnvelope returns an Envelope that stores data keys through pool. It takes
// a pool, not a caller's transaction, on purpose: a data key must be committed
// before any ciphertext made with it, or a rolled-back transaction would leave
// a cached key that exists nowhere else.
func NewEnvelope(pool *pgxpool.Pool, keys *Keyring, opts Options) (*Envelope, error) {
	if pool == nil || keys == nil {
		return nil, errors.New("crypto: envelope needs a pool and a keyring")
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = DefaultCacheTTL
	}
	if opts.CacheEntries <= 0 {
		opts.CacheEntries = DefaultCacheEntries
	}
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	return &Envelope{
		q:     store.New(pool),
		keys:  keys,
		cache: newDEKCache(opts.CacheTTL, opts.CacheEntries),
		clock: opts.Clock,
	}, nil
}

// Seal encrypts plaintext with the member's data key, creating the key on the
// member's first Seal. It returns ErrShredded after Shred.
func (e *Envelope) Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error) {
	if err := checkArgs(memberID, associatedData); err != nil {
		return nil, err
	}
	a, err := e.dek(ctx, memberID, true)
	if err != nil {
		return nil, err
	}
	ct, err := a.Encrypt(plaintext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("crypto: seal for member %s: %w", memberID, err)
	}
	return ct, nil
}

// Open decrypts a ciphertext made by Seal for the same member and associated
// data. It returns ErrInvalidCiphertext when they do not match and ErrShredded
// after Shred.
func (e *Envelope) Open(ctx context.Context, memberID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error) {
	if err := checkArgs(memberID, associatedData); err != nil {
		return nil, err
	}
	a, err := e.dek(ctx, memberID, false)
	if err != nil {
		return nil, err
	}
	pt, err := a.Decrypt(ciphertext, associatedData)
	if err != nil {
		// Do not wrap: the cause says nothing useful and must not grow details later.
		return nil, ErrInvalidCiphertext
	}
	return pt, nil
}

// Shred destroys the member's data key and evicts it from this process's
// cache. It is idempotent and also works for a member who never had a key.
func (e *Envelope) Shred(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("crypto: member id is required")
	}
	now := e.clock.Now()
	if err := e.q.ShredMemberKey(ctx, store.ShredMemberKeyParams{MemberID: memberID, DestroyedAt: &now}); err != nil {
		return fmt.Errorf("crypto: shred member %s: %w", memberID, err)
	}
	e.cache.markShredded(memberID, now)
	return nil
}

func checkArgs(memberID uuid.UUID, associatedData []byte) error {
	if memberID == uuid.Nil {
		return errors.New("crypto: member id is required")
	}
	if len(associatedData) == 0 {
		return errors.New("crypto: associated data is required (see AssociatedData)")
	}
	return nil
}

// dek returns the member's data key from the cache or the database. With
// create, a member without a key gets one.
func (e *Envelope) dek(ctx context.Context, memberID uuid.UUID, create bool) (tink.AEAD, error) {
	switch a, state := e.cache.get(memberID, e.clock.Now()); state {
	case cacheHit:
		return a, nil
	case cacheShredded:
		return nil, ErrShredded
	case cacheMiss:
	}
	row, err := e.q.GetMemberKey(ctx, memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		if !create {
			return nil, fmt.Errorf("%w: member has no data key", ErrInvalidCiphertext)
		}
		row, err = e.createDEK(ctx, memberID)
	}
	if err != nil {
		return nil, fmt.Errorf("crypto: load key of member %s: %w", memberID, err)
	}
	if row.DestroyedAt != nil {
		e.cache.markShredded(memberID, e.clock.Now())
		return nil, ErrShredded
	}
	a, err := e.unwrap(ctx, row)
	if err != nil {
		return nil, err
	}
	e.cache.put(memberID, a, e.clock.Now())
	return a, nil
}

// createDEK stores a new wrapped key unless another writer stored one first,
// then returns whatever row won.
func (e *Envelope) createDEK(ctx context.Context, memberID uuid.UUID) (store.PlatformMemberKey, error) {
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		return store.PlatformMemberKey{}, fmt.Errorf("generate key: %w", err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(h, keyset.NewBinaryWriter(&buf)); err != nil {
		return store.PlatformMemberKey{}, fmt.Errorf("serialize key: %w", err)
	}
	raw := buf.Bytes()
	kek := e.keys.Primary()
	wrapped, err := kek.Wrap(ctx, raw, wrapAD(memberID))
	clear(raw)
	if err != nil {
		return store.PlatformMemberKey{}, err
	}
	if err := e.q.InsertMemberKey(ctx, store.InsertMemberKeyParams{
		MemberID: memberID, WrappedDek: wrapped, KekID: kek.ID(),
	}); err != nil {
		return store.PlatformMemberKey{}, fmt.Errorf("insert key: %w", err)
	}
	row, err := e.q.GetMemberKey(ctx, memberID)
	if err != nil {
		return store.PlatformMemberKey{}, fmt.Errorf("re-read key: %w", err)
	}
	return row, nil
}

// wrapAD binds a wrapped data key to its member, so a wrapped_dek copied to
// another member's row does not unwrap.
func wrapAD(memberID uuid.UUID) []byte {
	return AssociatedData("platform.member_keys.wrapped_dek", memberID)
}

// unwrap opens the member's data key. A key still wrapped by a previous KEK
// is re-wrapped with the primary on the way (rewrap), so after a rotation
// the old KEK is needed only for members who have not been active since.
func (e *Envelope) unwrap(ctx context.Context, row store.PlatformMemberKey) (tink.AEAD, error) {
	kek, ok := e.keys.Get(row.KekID)
	if !ok {
		return nil, fmt.Errorf("crypto: key of member %s: unknown KEK %q", row.MemberID, row.KekID)
	}
	raw, err := kek.Unwrap(ctx, row.WrappedDek, wrapAD(row.MemberID))
	if err != nil {
		return nil, fmt.Errorf("crypto: key of member %s: %w", row.MemberID, err)
	}
	defer clear(raw)
	h, err := insecurecleartextkeyset.Read(keyset.NewBinaryReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, fmt.Errorf("crypto: key of member %s: unreadable keyset", row.MemberID)
	}
	if err := checkAES256GCM(h); err != nil {
		return nil, fmt.Errorf("crypto: key of member %s: %w", row.MemberID, err)
	}
	a, err := aead.New(h)
	if err != nil {
		return nil, fmt.Errorf("crypto: key of member %s: %w", row.MemberID, err)
	}
	if row.KekID != e.keys.Primary().ID() {
		e.rewrap(ctx, row.MemberID, row.KekID, raw)
	}
	return a, nil
}

// rewrap stores the member's data key wrapped by the primary KEK in place of
// the wrapping by from. The key itself does not change, so ciphertexts and
// cached copies stay valid. It is best effort: on failure the row keeps the
// old wrapping, which still opens, and the next load from the database
// tries again. The update applies only while the row is still wrapped by
// from and not shredded, so a concurrent rewrap or Shred wins.
func (e *Envelope) rewrap(ctx context.Context, memberID uuid.UUID, from string, raw []byte) {
	kek := e.keys.Primary()
	wrapped, err := kek.Wrap(ctx, raw, wrapAD(memberID))
	if err != nil {
		return
	}
	_, _ = e.q.RewrapMemberKey(ctx, store.RewrapMemberKeyParams{
		MemberID: memberID, WrappedDek: wrapped, KekID: kek.ID(), FromKekID: from,
	})
}
