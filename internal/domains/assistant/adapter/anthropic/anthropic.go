// Package anthropic implements the assistant LLM port with the official
// Anthropic Go SDK (Messages API).
//
// The request contains only what the service passes in model.LLMRequest,
// which is anonymized text (ADR-0013). Fragments and the member's text are
// framed as untrusted data: each is JSON-encoded inside its own tag
// (<knowledge_fragments>, <member_message>), and encoding/json escapes '<'
// and '>', so no data can close a tag and pose as instructions. The output
// is constrained to the reply JSON schema (output_config.format); the
// service still parses and validates it, and treats any failure as a
// provider failure.
//
// Failure behaviour: one overall deadline (Config.Timeout, default 20 s,
// docs/requirements/mvp.md § 5 step 7) covers the SDK's retries; every error
// is returned as *model.ProviderError with a kind for logs. Neither the
// request nor the response is ever logged.
//
// Environment variables of the SDK (ANTHROPIC_BASE_URL and others) are
// ignored on purpose: only Config decides where anonymized text may go.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
)

// Defaults for Config fields left zero.
const (
	DefaultModel      = "claude-opus-5-5"
	DefaultTimeout    = 20 * time.Second
	DefaultMaxTokens  = 4096
	DefaultEffort     = "low"
	DefaultMaxRetries = 1
)

// Config configures the adapter. APIKey comes from the secret store or the
// environment of the deployment, never from code or the repository.
type Config struct {
	APIKey string
	// BaseURL overrides the API endpoint (tests, or a gateway inside the
	// zero-data-retention agreement). Empty means the SDK default.
	BaseURL string
	// Model is the model ID (default claude-opus-5-5).
	Model string
	// Timeout bounds the whole call including retries (default 20 s).
	Timeout time.Duration
	// MaxTokens caps the response, thinking included (default 4096).
	MaxTokens int64
	// Effort is low, medium, high, xhigh or max (default low: a short chat
	// reply under a 20-second deadline).
	Effort string
	// MaxRetries is the SDK retry count on 408/409/429/5xx inside Timeout
	// (default 1; negative disables retries).
	MaxRetries int
	// HTTPClient replaces the SDK HTTP client (tests).
	HTTPClient *http.Client
}

// Client is the LLM port backed by the Anthropic Messages API.
type Client struct {
	client  sdk.Client
	model   string
	timeout time.Duration
	tokens  int64
	effort  sdk.OutputConfigEffort
}

var _ port.LLM = (*Client)(nil)

// New validates cfg and builds a client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("anthropic: API key is required")
	}
	c := &Client{
		model:   cfg.Model,
		timeout: cfg.Timeout,
		tokens:  cfg.MaxTokens,
		effort:  sdk.OutputConfigEffort(cfg.Effort),
	}
	if c.model == "" {
		c.model = DefaultModel
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.tokens <= 0 {
		c.tokens = DefaultMaxTokens
	}
	if c.effort == "" {
		c.effort = DefaultEffort
	}
	switch c.effort {
	case sdk.OutputConfigEffortLow, sdk.OutputConfigEffortMedium, sdk.OutputConfigEffortHigh,
		sdk.OutputConfigEffortXhigh, sdk.OutputConfigEffortMax:
	default:
		return nil, fmt.Errorf("anthropic: unknown effort %q", cfg.Effort)
	}
	retries := cfg.MaxRetries
	switch {
	case retries == 0:
		retries = DefaultMaxRetries
	case retries < 0:
		retries = 0
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(retries),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	c.client = sdk.NewClient(opts...)
	return c, nil
}

// ModelID returns the configured model ID (for logs and evaluation reports).
func (c *Client) ModelID() string { return c.model }

// outputSchema is the reply contract {reply, crisis, sources, suggest}.
// Length and subset rules are checked by the service (model.Validate).
var outputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"reply", "crisis", "sources", "suggest"},
	"properties": map[string]any{
		"reply":   map[string]any{"type": "string"},
		"crisis":  map[string]any{"type": "boolean"},
		"sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"suggest": map[string]any{
			"type": "string",
			"enum": []string{model.SuggestNone, model.SuggestTea, model.SuggestPsychologist, model.SuggestRights, model.SuggestPractice},
		},
	},
}

// Complete sends one request and returns the raw JSON text of the reply.
func (c *Client) Complete(ctx context.Context, req model.LLMRequest) (model.Completion, error) {
	out := model.Completion{ModelID: c.model}
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return out, &model.ProviderError{Kind: model.FailureUnavailable, Err: errors.New("empty system prompt")}
	}
	messages, err := buildMessages(req)
	if err != nil {
		return out, &model.ProviderError{Kind: model.FailureAPIError, Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	params := sdk.MessageNewParams{
		Model:     c.model,
		MaxTokens: c.tokens,
		// The prompt is stable across members: cache it (ADR-0017, cost).
		System: []sdk.TextBlockParam{{
			Text:         req.SystemPrompt,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}},
		Messages: messages,
		Thinking: sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{}},
		OutputConfig: sdk.OutputConfigParam{
			Effort: c.effort,
			Format: sdk.JSONOutputFormatParam{Schema: outputSchema},
		},
	}
	resp, err := c.client.Messages.New(ctx, params, option.WithRequestTimeout(c.timeout))
	if err != nil {
		return out, classify(ctx, err)
	}
	if resp.Model != "" {
		out.ModelID = resp.Model
	}
	switch resp.StopReason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
	case sdk.StopReasonRefusal:
		return out, &model.ProviderError{Kind: model.FailureRefusal}
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		return out, &model.ProviderError{Kind: model.FailureTruncated}
	default:
		return out, &model.ProviderError{Kind: model.FailureInvalidOutput, Err: fmt.Errorf("stop reason %q", resp.StopReason)}
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(sdk.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	out.JSON = strings.TrimSpace(b.String())
	if out.JSON == "" {
		return out, &model.ProviderError{Kind: model.FailureInvalidOutput, Err: errors.New("no text block")}
	}
	return out, nil
}

func classify(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &model.ProviderError{Kind: model.FailureTimeout, Err: err}
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		// The SDK error text includes the response body, which may echo the
		// request; keep only the status code.
		status := fmt.Errorf("status %d", apiErr.StatusCode)
		if apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode == 529 {
			return &model.ProviderError{Kind: model.FailureRateLimited, Err: status}
		}
		return &model.ProviderError{Kind: model.FailureAPIError, Err: status}
	}
	return &model.ProviderError{Kind: model.FailureUnavailable, Err: errors.New("transport error")}
}

type fragmentJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// buildMessages turns history and the current message into alternating
// user/assistant turns. Member text is wrapped as data; the request starts
// with a user turn and ends with the current one (no assistant prefill).
func buildMessages(req model.LLMRequest) ([]sdk.MessageParam, error) {
	var msgs []sdk.MessageParam
	for _, h := range req.History {
		switch h.Role {
		case model.RoleMember:
			wrapped, err := memberBlock(req.Lang, h.Text)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, sdk.NewUserMessage(sdk.NewTextBlock(wrapped)))
		case model.RoleAssistant:
			if len(msgs) == 0 {
				continue
			}
			msgs = append(msgs, sdk.NewAssistantMessage(sdk.NewTextBlock(h.Text)))
		}
	}
	frags := make([]fragmentJSON, 0, len(req.Fragments))
	for _, f := range req.Fragments {
		frags = append(frags, fragmentJSON{ID: f.ArticleID.String(), Title: f.Title, Text: f.Text})
	}
	fragJSON, err := json.Marshal(frags)
	if err != nil {
		return nil, fmt.Errorf("encode fragments: %w", err)
	}
	member, err := memberBlock(req.Lang, req.Text)
	if err != nil {
		return nil, err
	}
	scope := "general"
	if req.Scope == model.ScopeRights {
		scope = "rights (legal questions; answer only from the fragments)"
	}
	current := "Interface language: " + req.Lang + ". Scope: " + scope + ".\n" +
		"<knowledge_fragments>\n" + string(fragJSON) + "\n</knowledge_fragments>\n" + member
	msgs = append(msgs, sdk.NewUserMessage(sdk.NewTextBlock(current)))
	return msgs, nil
}

func memberBlock(lang, text string) (string, error) {
	b, err := json.Marshal(struct {
		Lang string `json:"lang"`
		Text string `json:"text"`
	}{lang, text})
	if err != nil {
		return "", fmt.Errorf("encode member message: %w", err)
	}
	return "<member_message>\n" + string(b) + "\n</member_message>", nil
}
