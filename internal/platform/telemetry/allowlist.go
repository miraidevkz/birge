package telemetry

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Attribute keys Birge code may put on a span. Every other key is dropped by
// the allow-list, and these are dropped too when their value does not have
// the expected shape (a phone in an ID field, a raw path in http.route).
const (
	KeyHTTPMethod      = "http.request.method"
	KeyHTTPRoute       = "http.route"
	KeyHTTPStatus      = "http.response.status_code"
	KeyDBSystem        = "db.system.name"
	KeyDBQueryName     = "db.query.name"
	KeyMessagingSystem = "messaging.system"
	KeyMessagingOp     = "messaging.operation.type"
	KeyMessagingDest   = "messaging.destination.name"
	KeyMessagingMsgID  = "messaging.message.id"
	KeyMessagingGroup  = "messaging.consumer.group.name"
	KeyEventType       = "birge.event.type"
	KeyEventID         = "birge.event.id"
	KeyOutcome         = "birge.outcome"
	KeyErrorType       = "error.type"
)

var (
	// eventTypeRe matches <context>.<EventName> (the outbox event type).
	eventTypeRe = regexp.MustCompile(`^[a-z]+\.[A-Z][A-Za-z]+$`)
	// subjectRe matches the broker subject of a domain event (outbox.Subject).
	subjectRe = regexp.MustCompile(`^birge\.events\.[a-z]+\.[A-Z][A-Za-z]+$`)
	// queryNameRe matches an sqlc query name or a bare transaction keyword.
	queryNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	// tokenRe matches a short code: a durable consumer name, an SQLSTATE, an
	// error class. No spaces, no punctuation that could carry a value.
	tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// routeSegmentRe matches one segment of a route template: a literal of
	// lowercase letters (no digits: an ID or a phone always has some), the
	// API version, a {wildcard}, or the empty segment of a trailing slash.
	routeSegmentRe = regexp.MustCompile(`^(|v1|[a-z][a-z._-]*|\{[A-Za-z]+(\.\.\.)?\}|\{\$\})$`)
)

// httpMethods are the methods a route or span may name; anything a client
// invents is reported as OTHER by the HTTP middleware and stripped here.
var httpMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true, "OTHER": true,
}

// allowed maps every permitted key to the check its value must pass.
var allowed = map[string]func(attribute.Value) bool{
	KeyHTTPMethod: func(v attribute.Value) bool { return v.Type() == attribute.STRING && httpMethods[v.AsString()] },
	KeyHTTPRoute:  func(v attribute.Value) bool { return v.Type() == attribute.STRING && IsRouteTemplate(v.AsString()) },
	KeyHTTPStatus: func(v attribute.Value) bool {
		return v.Type() == attribute.INT64 && v.AsInt64() >= 100 && v.AsInt64() <= 599
	},
	KeyDBSystem:        oneOf("postgresql"),
	KeyDBQueryName:     matches(queryNameRe),
	KeyMessagingSystem: oneOf("nats"),
	KeyMessagingOp:     oneOf("publish", "process"),
	KeyMessagingDest:   matches(subjectRe),
	KeyMessagingMsgID:  isUUID,
	KeyMessagingGroup:  matches(tokenRe),
	KeyEventType:       matches(eventTypeRe),
	KeyEventID:         isUUID,
	KeyOutcome:         oneOf("ok", "error", "ack", "nak", "term", "unacked"),
	KeyErrorType:       matches(tokenRe),
}

func oneOf(values ...string) func(attribute.Value) bool {
	return func(v attribute.Value) bool {
		if v.Type() != attribute.STRING {
			return false
		}
		for _, s := range values {
			if v.AsString() == s {
				return true
			}
		}
		return false
	}
}

func matches(re *regexp.Regexp) func(attribute.Value) bool {
	return func(v attribute.Value) bool { return v.Type() == attribute.STRING && re.MatchString(v.AsString()) }
}

func isUUID(v attribute.Value) bool {
	if v.Type() != attribute.STRING {
		return false
	}
	_, err := uuid.Parse(v.AsString())
	return err == nil && len(v.AsString()) == 36
}

// Allowed reports whether kv may leave the process on a span: its key is on
// the allow-list and its value has the expected shape. This is the single
// rule for span data (ADR-0021): IDs, event names, route templates, status
// codes and outcomes; never text, names, phones, tokens, URLs or SQL.
func Allowed(kv attribute.KeyValue) bool {
	check, ok := allowed[string(kv.Key)]
	return ok && check(kv.Value)
}

// IsRouteTemplate reports whether s is a ServeMux pattern ("POST
// /v1/me/sos", "GET /v1/me/children/{childId}", "/auth/") or "unmatched",
// and not a raw request path: no query, no fragment, and no segment that
// could hold an ID, a phone or a token.
func IsRouteTemplate(s string) bool {
	if s == "unmatched" {
		return true
	}
	path := s
	if method, rest, ok := strings.Cut(s, " "); ok {
		if !httpMethods[method] {
			return false
		}
		path = rest
	}
	if !strings.HasPrefix(path, "/") || len(path) > 128 || strings.ContainsAny(path, "?#% ") {
		return false
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if !routeSegmentRe.MatchString(seg) {
			return false
		}
	}
	return true
}

// spanNameRe matches the names Birge gives spans: a route template is
// checked separately; the others are "db <QueryName>", "publish <event
// type>" and "consume <event type>".
var spanNameRe = regexp.MustCompile(`^(db [A-Za-z][A-Za-z0-9_]{0,63}|(publish|consume) [a-z]+\.[A-Z][A-Za-z]+|HTTP [A-Z]+)$`)

// redactedSpanName replaces a span name that is neither a route template
// nor one of the fixed shapes, so a name built from data never leaves.
const redactedSpanName = "span"

func safeSpanName(name string) string {
	if IsRouteTemplate(name) || spanNameRe.MatchString(name) {
		return name
	}
	return redactedSpanName
}

func filterAttrs(in []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(in))
	for _, kv := range in {
		if Allowed(kv) {
			out = append(out, kv)
		}
	}
	return out
}

// allowListProcessor applies the allow-list to every finished span before
// the next processor (the batcher and the exporter) sees it. It is the only
// path from a span to an exporter (NewTracerProvider), so instrumentation
// added later, ours or a library's, cannot export a forbidden attribute.
type allowListProcessor struct {
	next sdktrace.SpanProcessor
}

// NewAllowListProcessor wraps next so that it only receives spans reduced
// to allowed attributes, with status descriptions, event attributes and
// link attributes filtered the same way.
func NewAllowListProcessor(next sdktrace.SpanProcessor) sdktrace.SpanProcessor {
	return &allowListProcessor{next: next}
}

// OnStart passes the span on unchanged: its attributes are filtered when it
// ends, which also covers attributes set after the start.
func (p *allowListProcessor) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(ctx, s)
}

// OnEnd hands the filtered span to the next processor.
func (p *allowListProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.next.OnEnd(filteredSpan{ReadOnlySpan: s})
}

// Shutdown shuts down the next processor.
func (p *allowListProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx) //nolint:wrapcheck // passed through unchanged
}

// ForceFlush flushes the next processor.
func (p *allowListProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx) //nolint:wrapcheck // passed through unchanged
}

// filteredSpan is a read-only view of a span with only allowed data. It
// embeds the original span for everything that cannot carry personal data
// (span context, times, kind, scope, resource).
type filteredSpan struct {
	sdktrace.ReadOnlySpan
}

// Name returns the span name, or a fixed placeholder if it is not one of
// the shapes Birge uses.
func (s filteredSpan) Name() string { return safeSpanName(s.ReadOnlySpan.Name()) }

// Attributes returns only the allowed attributes.
func (s filteredSpan) Attributes() []attribute.KeyValue {
	return filterAttrs(s.ReadOnlySpan.Attributes())
}

// Events keeps the event names and time but filters their attributes: an
// exception event's message and stack can quote data.
func (s filteredSpan) Events() []sdktrace.Event {
	in := s.ReadOnlySpan.Events()
	out := make([]sdktrace.Event, 0, len(in))
	for _, e := range in {
		e.Name = safeEventName(e.Name)
		e.Attributes = filterAttrs(e.Attributes)
		e.DroppedAttributeCount = 0
		out = append(out, e)
	}
	return out
}

// eventNameRe matches conventional event names ("exception", "retry"):
// lowercase words only, so a number or a value cannot pass as a name.
var eventNameRe = regexp.MustCompile(`^[a-z][a-z_.]{0,63}$`)

// safeEventName keeps the conventional event names and replaces any other.
func safeEventName(name string) string {
	if eventNameRe.MatchString(name) {
		return name
	}
	return "event"
}

// Links keeps the linked span contexts and filters the link attributes.
func (s filteredSpan) Links() []sdktrace.Link {
	in := s.ReadOnlySpan.Links()
	out := make([]sdktrace.Link, 0, len(in))
	for _, l := range in {
		l.Attributes = filterAttrs(l.Attributes)
		out = append(out, l)
	}
	return out
}

// Status keeps the status code and drops its description, which is usually
// an error message (a pgx error quotes the failing value).
func (s filteredSpan) Status() sdktrace.Status {
	return sdktrace.Status{Code: s.ReadOnlySpan.Status().Code}
}
