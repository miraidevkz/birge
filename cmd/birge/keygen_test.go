package main

import (
	"os"
	"path/filepath"
	"testing"

	"birge/internal/platform/crypto"
)

func TestWriteDevKeys(t *testing.T) {
	dir := t.TempDir()
	if err := writeDevKeys(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kek.json", "pow.key", "hash.key", "blind-index.key", "plan-link.key"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Mode().Perm() != keygenFileMode {
			t.Errorf("%s mode = %o, want %o", name, info.Mode().Perm(), keygenFileMode)
		}
	}
	kekPath := filepath.Join(dir, "kek.json")
	raw, err := os.ReadFile(kekPath)
	if err != nil {
		t.Fatal(err)
	}
	k, err := crypto.NewLocalKEK(keygenKEKID, raw)
	if err != nil {
		t.Fatalf("NewLocalKEK: %v", err)
	}
	if k.ID() != keygenKEKID {
		t.Errorf("KEK id = %q, want %q", k.ID(), keygenKEKID)
	}
	if err := writeDevKeys(dir); err == nil {
		t.Fatal("overwrite of existing files accepted")
	}
}
