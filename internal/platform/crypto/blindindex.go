package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// BlindIndexer computes blind indexes: keyed hashes that let a context find a
// row by an encrypted value (for example a phone number) without decrypting.
// Its key comes from the secret store and must be separate from every KEK.
type BlindIndexer struct {
	key []byte
}

// NewBlindIndexer returns an indexer with a copy of key (at least 32 bytes).
func NewBlindIndexer(key []byte) (*BlindIndexer, error) {
	if len(key) < 32 {
		return nil, errors.New("crypto: blind index key must be at least 32 bytes")
	}
	return &BlindIndexer{key: append([]byte(nil), key...)}, nil
}

// BlindIndex returns HMAC-SHA-256 of value, hex-encoded, under a key derived
// for kind (e.g. "membership.phone"), so equal values of different kinds never
// share an index. Callers normalize value first (e.g. E.164 for phones);
// the index is only as good as the normalization.
func (b *BlindIndexer) BlindIndex(kind, value string) string {
	derive := hmac.New(sha256.New, b.key)
	derive.Write([]byte("birge.blind-index.v1|" + kind))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
