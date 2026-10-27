package retrieval_test

import (
	"strings"
	"testing"

	"birge/internal/domains/knowledge/retrieval"
)

func TestChunkEmpty(t *testing.T) {
	if got := retrieval.Chunk(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	if got := retrieval.Chunk("   \n\t"); len(got) != 0 {
		t.Fatalf("whitespace: %v", got)
	}
}

func TestChunkShort(t *testing.T) {
	got := retrieval.Chunk("hello")
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %#v", got)
	}
}

func TestChunkOverlap(t *testing.T) {
	body := strings.Repeat("а", retrieval.ChunkSize+50) // multi-byte runes
	got := retrieval.Chunk(body)
	if len(got) < 2 {
		t.Fatalf("want ≥2 chunks, got %d", len(got))
	}
	if retrieval.RuneCount(got[0]) != retrieval.ChunkSize {
		t.Fatalf("first chunk runes = %d", retrieval.RuneCount(got[0]))
	}
	// Overlap: the end of chunk 0 matches the start of chunk 1.
	overlap := string([]rune(got[0])[retrieval.ChunkSize-retrieval.ChunkOverlap:])
	prefix := string([]rune(got[1])[:retrieval.ChunkOverlap])
	if overlap != prefix {
		t.Fatalf("overlap mismatch")
	}
}
