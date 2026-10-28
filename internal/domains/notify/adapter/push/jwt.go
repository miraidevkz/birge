package push

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
	"errors"
	"fmt"
)

// signJWT builds a compact JWS: base64url(header).base64url(claims).base64url(sig).
// Only the two algorithms the providers require exist here (RS256 for the
// Google token endpoint, ES256 for APNs); there is no "alg" negotiation, so
// none of the classic JWT algorithm-confusion problems apply.
func signJWT(header, claims any, sign func(digest []byte) ([]byte, error)) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("encode JWT header: %w", err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode JWT claims: %w", err)
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	digest := sha256.Sum256([]byte(signing))
	sig, err := sign(digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// rs256 signs a SHA-256 digest with RSASSA-PKCS1-v1_5 (JWA RS256).
func rs256(key *rsa.PrivateKey) func([]byte) ([]byte, error) {
	return func(digest []byte) ([]byte, error) {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest)
		if err != nil {
			return nil, fmt.Errorf("RS256: %w", err)
		}
		return sig, nil
	}
}

// es256 signs a SHA-256 digest with ECDSA P-256 and returns the fixed-width
// r||s form JWA ES256 requires (not the ASN.1 form crypto/ecdsa emits by
// default; APNs rejects that as InvalidProviderToken).
func es256(key *ecdsa.PrivateKey) func([]byte) ([]byte, error) {
	return func(digest []byte) ([]byte, error) {
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			return nil, fmt.Errorf("ES256: %w", err)
		}
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out, nil
	}
}

// errBadKey is returned for unusable key material. Its text never includes
// the material itself.
var errBadKey = errors.New("push: unusable private key")

// parseRSAKey reads a PEM RSA private key (PKCS#8, as Google issues it in a
// service-account file, or PKCS#1).
func parseRSAKey(pemText []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemText)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block", errBadKey)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: not an RSA key", errBadKey)
		}
		return rk, nil
	}
	rk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: not a PKCS#8 or PKCS#1 RSA key", errBadKey)
	}
	return rk, nil
}

// parseP8Key reads the APNs auth key (.p8): a PEM PKCS#8 ECDSA P-256 key.
func parseP8Key(pemText []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemText)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block", errBadKey)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: not a PKCS#8 key", errBadKey)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: not an ECDSA P-256 key", errBadKey)
	}
	return ek, nil
}
