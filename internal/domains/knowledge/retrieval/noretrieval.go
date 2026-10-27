package retrieval

import "context"

// NoRetrieval is the fallback when TEI or Qdrant are not configured: every
// search returns no fragments (the assistant then says it does not know).
// Wiring logs Warn event=RetrievalDisabled once at process start.
type NoRetrieval struct{}

// Search always returns an empty hit list.
func (NoRetrieval) Search(context.Context, string, string, string, int) ([]Hit, error) {
	return nil, nil
}
