package retrieval

import "unicode/utf8"

// ChunkSize is the target size of one embedded passage in Unicode characters
// (runes). Overlap keeps a little context across chunk boundaries.
const (
	ChunkSize    = 800
	ChunkOverlap = 100
)

// Chunk splits body into overlapping rune windows of at most ChunkSize.
// Empty or whitespace-only bodies yield no chunks. Overlap is clamped so
// progress is always made (step = size - overlap ≥ 1).
func Chunk(body string) []string {
	runes := []rune(body)
	if len(runes) == 0 {
		return nil
	}
	// Drop leading/trailing whitespace-only content cheaply: if the whole
	// body trims to empty, nothing to index.
	if trimRunesEmpty(runes) {
		return nil
	}
	size := ChunkSize
	overlap := ChunkOverlap
	if overlap >= size {
		overlap = size - 1
	}
	step := size - overlap
	var out []string
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return out
}

func trimRunesEmpty(runes []rune) bool {
	for _, r := range runes {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// RuneCount is utf8.RuneCountInString exposed for tests.
func RuneCount(s string) int { return utf8.RuneCountInString(s) }
