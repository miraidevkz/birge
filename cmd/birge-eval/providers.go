package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
)

// fakeModelID is the model ID of every offline provider.
const fakeModelID = "offline-fake"

// fakeReplyJSON is a valid model output (contract AssistantReply) without
// any member data, so an anonymization case can run the whole pipeline.
const fakeReplyJSON = `{"reply":"eval reply","crisis":false,"sources":[],"suggest":"none"}`

// Provider failure modes of the llm-down set. modeUnavailable means no
// provider is configured at all (missing API key).
const (
	modeAPIError      = model.FailureAPIError
	modeTimeout       = model.FailureTimeout
	modeRateLimited   = model.FailureRateLimited
	modeRefusal       = model.FailureRefusal
	modeInvalidOutput = model.FailureInvalidOutput
	modeTruncated     = model.FailureTruncated
	modeUnavailable   = model.FailureUnavailable
)

var llmDownModes = []string{modeAPIError, modeTimeout, modeRateLimited, modeRefusal, modeInvalidOutput, modeTruncated, modeUnavailable}

var errProviderDown = errors.New("eval: provider down")

// failingLLM is a provider that never produces a usable answer: it fails
// with the mode's provider error, or answers with output that breaks the
// JSON contract. The crisis set runs only with it.
type failingLLM struct {
	mode string
}

// Complete implements port.LLM and always fails.
func (f failingLLM) Complete(_ context.Context, _ model.LLMRequest) (model.Completion, error) {
	switch f.mode {
	case modeInvalidOutput:
		return model.Completion{JSON: "I am not JSON", ModelID: fakeModelID}, nil
	case modeTruncated:
		return model.Completion{JSON: `{"reply":"cut off`, ModelID: fakeModelID}, nil
	case modeTimeout:
		return model.Completion{ModelID: fakeModelID}, &model.ProviderError{Kind: modeTimeout, Err: context.DeadlineExceeded}
	case modeRateLimited, modeRefusal:
		return model.Completion{ModelID: fakeModelID}, &model.ProviderError{Kind: f.mode, Err: errProviderDown}
	default:
		return model.Completion{ModelID: fakeModelID}, &model.ProviderError{Kind: modeAPIError, Err: errProviderDown}
	}
}

// fixedLLM answers every request with JSON.
type fixedLLM struct {
	JSON string
}

// Complete implements port.LLM.
func (f fixedLLM) Complete(_ context.Context, _ model.LLMRequest) (model.Completion, error) {
	return model.Completion{JSON: f.JSON, ModelID: fakeModelID}, nil
}

// recordingLLM records every request before passing it to inner, so a set
// can see exactly what would have left the jurisdiction and count calls.
type recordingLLM struct {
	inner port.LLM

	mu       sync.Mutex
	requests []model.LLMRequest
}

// Complete implements port.LLM.
func (r *recordingLLM) Complete(ctx context.Context, req model.LLMRequest) (model.Completion, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.inner.Complete(ctx, req) //nolint:wrapcheck // provider errors pass through unchanged for the service to classify
}

// since returns the requests recorded after the first n.
func (r *recordingLLM) since(n int) []model.LLMRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.LLMRequest(nil), r.requests[n:]...)
}

func (r *recordingLLM) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// budgetLLM caps the live provider calls of one run (ADR-0017: budget cap
// per run). Over the cap it fails without calling the provider, and the run
// exits 2 because its report is incomplete.
type budgetLLM struct {
	inner   port.LLM
	max     int
	modelID string

	mu    sync.Mutex
	calls int
	over  bool
}

// Complete implements port.LLM.
func (b *budgetLLM) Complete(ctx context.Context, req model.LLMRequest) (model.Completion, error) {
	b.mu.Lock()
	if b.calls >= b.max {
		b.over = true
		b.mu.Unlock()
		return model.Completion{}, &model.ProviderError{Kind: modeUnavailable, Err: fmt.Errorf("eval: budget of %d calls exhausted", b.max)}
	}
	b.calls++
	b.mu.Unlock()
	comp, err := b.inner.Complete(ctx, req)
	if comp.ModelID != "" {
		b.mu.Lock()
		b.modelID = comp.ModelID
		b.mu.Unlock()
	}
	return comp, err //nolint:wrapcheck // provider errors pass through unchanged for the service to classify
}

// model returns the last model ID the provider reported.
func (b *budgetLLM) model() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelID
}

func (b *budgetLLM) exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

var (
	_ port.LLM = failingLLM{}
	_ port.LLM = fixedLLM{}
	_ port.LLM = (*recordingLLM)(nil)
	_ port.LLM = (*budgetLLM)(nil)
)
