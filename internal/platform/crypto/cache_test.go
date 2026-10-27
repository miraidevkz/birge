package crypto

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"
)

func testAEAD(t *testing.T) tink.AEAD {
	t.Helper()
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		t.Fatal(err)
	}
	a, err := aead.New(h)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDEKCacheExpiry(t *testing.T) {
	now := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	c := newDEKCache(time.Minute, 10)
	id := uuid.New()
	c.put(id, testAEAD(t), now)
	if _, st := c.get(id, now.Add(59*time.Second)); st != cacheHit {
		t.Fatalf("before expiry: state %v", st)
	}
	if _, st := c.get(id, now.Add(time.Minute)); st != cacheMiss {
		t.Fatalf("after expiry: state %v", st)
	}
	if c.len() != 0 {
		t.Fatal("expired entry kept")
	}
}

func TestDEKCacheBound(t *testing.T) {
	now := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	c := newDEKCache(time.Minute, 3)
	a := testAEAD(t)
	marked := uuid.New()
	c.markShredded(marked, now)
	for range 10 {
		c.put(uuid.New(), a, now)
		if c.len() > 3 {
			t.Fatalf("cache holds %d entries, max 3", c.len())
		}
	}
	if _, st := c.get(marked, now); st != cacheShredded {
		t.Fatal("shredded marker evicted before live keys")
	}
}

func TestDEKCacheShreddedMarkerWins(t *testing.T) {
	now := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	c := newDEKCache(time.Minute, 10)
	id := uuid.New()
	c.put(id, testAEAD(t), now)
	c.markShredded(id, now)
	// A load that read the row before Shred must not cache the old key.
	c.put(id, testAEAD(t), now)
	if _, st := c.get(id, now); st != cacheShredded {
		t.Fatalf("state %v, want shredded", st)
	}
	if _, st := c.get(id, now.Add(time.Minute)); st != cacheMiss {
		t.Fatalf("marker did not expire: state %v", st)
	}
}
