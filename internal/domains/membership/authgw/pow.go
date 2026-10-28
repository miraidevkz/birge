package authgw

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"birge/internal/platform/ratelimit"
)

// powExpiry is how long an issued challenge stays solvable.
const powExpiry = 5 * time.Minute

const powAlgorithm = "SHA-256"

// challenge is the ALTCHA challenge JSON returned by GET /auth/pow.
type challenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	MaxNumber int64  `json:"maxnumber"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

// solution is the ALTCHA payload: base64 of this JSON, sent in the PoW header.
type solution struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Number    int64  `json:"number"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

type pow struct {
	key       []byte
	maxNumber int64
	// replay holds a claim per accepted challenge until it expires; shared
	// by every replica with the PostgreSQL store.
	replay ratelimit.Store
}

func (p *pow) sign(challengeHex string) string {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(challengeHex))
	return hex.EncodeToString(m.Sum(nil))
}

func hashChallenge(salt string, number int64) string {
	sum := sha256.Sum256([]byte(salt + strconv.FormatInt(number, 10)))
	return hex.EncodeToString(sum[:])
}

// issue creates a signed challenge that expires powExpiry after now.
func (p *pow) issue(now time.Time) (challenge, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return challenge{}, fmt.Errorf("pow salt: %w", err)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(p.maxNumber+1))
	if err != nil {
		return challenge{}, fmt.Errorf("pow number: %w", err)
	}
	salt := hex.EncodeToString(raw[:]) + "?expires=" + strconv.FormatInt(now.Add(powExpiry).Unix(), 10)
	c := hashChallenge(salt, n.Int64())
	return challenge{Algorithm: powAlgorithm, Challenge: c, MaxNumber: p.maxNumber, Salt: salt, Signature: p.sign(c)}, nil
}

var (
	errPoWMissing = errors.New("pow solution missing")
	errPoWInvalid = errors.New("pow solution invalid")
	errPoWExpired = errors.New("pow solution expired")
	errPoWReplay  = errors.New("pow solution already used")
	errPoWStore   = errors.New("pow replay store unavailable")
)

// verify checks an encoded solution and marks it used. Each solution is
// accepted once, across replicas when the replay store is shared. A store
// failure is errPoWStore: the caller refuses the request (fails closed).
func (p *pow) verify(ctx context.Context, encoded string, now time.Time) error {
	if encoded == "" {
		return errPoWMissing
	}
	if len(encoded) > 2048 {
		return errPoWInvalid
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errPoWInvalid
	}
	var s solution
	if err := json.Unmarshal(raw, &s); err != nil {
		return errPoWInvalid
	}
	if s.Algorithm != powAlgorithm || s.Number < 0 || s.Number > p.maxNumber {
		return errPoWInvalid
	}
	expires, ok := saltExpiry(s.Salt)
	if !ok {
		return errPoWInvalid
	}
	// Signature first: it proves we issued this salt and challenge.
	if !hmac.Equal([]byte(s.Signature), []byte(p.sign(s.Challenge))) {
		return errPoWInvalid
	}
	if !hmac.Equal([]byte(s.Challenge), []byte(hashChallenge(s.Salt, s.Number))) {
		return errPoWInvalid
	}
	if !now.Before(expires) {
		return errPoWExpired
	}
	// Claimed last, after every check: only a valid, unexpired solution
	// uses a row, so forged solutions cannot fill the store.
	fresh, err := p.replay.Claim(ctx, "auth.pow|"+s.Challenge, expires, now)
	if err != nil {
		return fmt.Errorf("%w: %w", errPoWStore, err)
	}
	if !fresh {
		return errPoWReplay
	}
	return nil
}

// saltExpiry reads the "expires" unix time from an ALTCHA salt "<random>?expires=<unix>".
func saltExpiry(salt string) (time.Time, bool) {
	_, params, ok := strings.Cut(salt, "?")
	if !ok {
		return time.Time{}, false
	}
	q, err := url.ParseQuery(params)
	if err != nil {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(q.Get("expires"), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}
