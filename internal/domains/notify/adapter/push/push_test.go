package push_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is a settable clock.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *testClock {
	return &testClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
}

// jwtParts decodes header and claims of a compact JWS and returns the
// signing input and signature.
func jwtParts(t *testing.T, token string) (header, claims map[string]any, signing string, sig []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts", len(parts))
	}
	dec := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("JWT part is not base64url: %v", err)
		}
		return b
	}
	if err := json.Unmarshal(dec(parts[0]), &header); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(dec(parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	return header, claims, parts[0] + "." + parts[1], dec(parts[2])
}

func verifyRS256(t *testing.T, pub *rsa.PublicKey, signing string, sig []byte) {
	t.Helper()
	d := sha256.Sum256([]byte(signing))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, d[:], sig); err != nil {
		t.Errorf("RS256 signature does not verify: %v", err)
	}
}

func verifyES256(t *testing.T, pub *ecdsa.PublicKey, signing string, sig []byte) {
	t.Helper()
	if len(sig) != 64 {
		t.Fatalf("ES256 signature is %d bytes, want 64 (r||s)", len(sig))
	}
	d := sha256.Sum256([]byte(signing))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, d[:], r, s) {
		t.Error("ES256 signature does not verify")
	}
}

// testRSAKey returns a fresh key and its PKCS#8 PEM. Keys are generated per
// test run; none is stored in the repository.
func testRSAKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func testP8Key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
