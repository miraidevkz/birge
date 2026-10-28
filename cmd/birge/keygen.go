package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
)

// Development KEK id printed for BIRGE_KEK_ID. Production keys come from the
// secret store with operator-chosen ids; never reuse an id for other material.
const keygenKEKID = "local-1"

const keygenFileMode = 0o600

// runKeygen writes local development key files into dir: kek.json (cleartext
// Tink AES256-GCM keyset for BIRGE_KEK_KEYSET_FILE) and pow.key, hash.key,
// blind-index.key, plan-link.key, analytics.key (32 random bytes, base64 text).
// Existing files are refused.
// It prints only the file names and the KEK id to use.
func runKeygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "directory for the key files (created if missing)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: birge keygen -dir <dir>")
		return 2
	}
	if err := writeDevKeys(*dir); err != nil {
		fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
		return 1
	}
	return 0
}

func writeDevKeys(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	kekJSON, err := newKEKKeysetJSON()
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{"kek.json", kekJSON},
		{"pow.key", randomKeyB64()},
		{"hash.key", randomKeyB64()},
		{"blind-index.key", randomKeyB64()},
		{"plan-link.key", randomKeyB64()},
		{"analytics.key", randomKeyB64()},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := writeNewFile(path, f.data); err != nil {
			return err
		}
		fmt.Println(f.name)
	}
	fmt.Println("BIRGE_KEK_ID=" + keygenKEKID)
	return nil
}

func writeNewFile(path string, data []byte) error {
	// path is filepath.Join(user -dir, fixed basename); O_EXCL refuses overwrite.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keygenFileMode) //nolint:gosec // G304: path is -dir plus a fixed key file name
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite %s", path)
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func newKEKKeysetJSON() ([]byte, error) {
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		return nil, fmt.Errorf("create KEK keyset: %w", err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(h, keyset.NewJSONWriter(&buf)); err != nil {
		return nil, fmt.Errorf("encode KEK keyset: %w", err)
	}
	return buf.Bytes(), nil
}

func randomKeyB64() []byte {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("keygen: crypto/rand: " + err.Error())
	}
	out := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(out, raw)
	return out
}
