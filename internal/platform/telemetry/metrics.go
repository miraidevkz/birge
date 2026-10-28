package telemetry

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds every Birge metric. It is the process registry served by
// Handler; a private registry (not the client's global one) keeps library
// metrics we did not review out of /metrics.
//
// Labels are bounded sets chosen in code: route templates, status codes,
// event types, consumer names, guard names and fixed reasons. Never a
// member, escalation or any other ID: an ID label would make every member
// a time series (cardinality) and put identifiers into the metrics store
// (ADR-0021).
var Registry = prometheus.NewRegistry()

// birgeCollectors are the Birge metrics registered through factory; the
// alert-rules test describes them to check every metric an alert names.
var birgeCollectors []prometheus.Collector

var factory = func() func(prometheus.Collector) {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return func(c prometheus.Collector) {
		Registry.MustRegister(c)
		birgeCollectors = append(birgeCollectors, c)
	}
}()

func counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	factory(c)
	return c
}

func counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	factory(c)
	return c
}

func gaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	factory(g)
	return g
}

func gauge(name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	factory(g)
	return g
}

// Metrics. Names follow Prometheus conventions (base units, _total for
// counters). deploy/prometheus/alerts.yml refers to them by name, and a test
// checks that every metric an alert uses is registered here.
var (
	httpRequests = counterVec("birge_http_requests_total",
		"HTTP requests by route template and status code.", "route", "status")
	httpDuration = func() *prometheus.HistogramVec {
		h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "birge_http_request_duration_seconds",
			Help:    "HTTP request latency by route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"route"})
		factory(h)
		return h
	}()

	// OutboxPublished counts events the relay published, by event type.
	OutboxPublished = counterVec("birge_outbox_published_total",
		"Outbox events published to the broker, by event type.", "event_type")
	// OutboxPublishErrors counts failed relay batches.
	OutboxPublishErrors = counter("birge_outbox_publish_errors_total",
		"Outbox relay batches that failed to claim, publish or mark.")
	// EventsConsumed counts handled broker messages by consumer, event type
	// and outcome (ack, nak, term, unacked).
	EventsConsumed = counterVec("birge_events_consumed_total",
		"Domain events handled by a consumer, by consumer, event type and outcome.", "consumer", "event_type", "outcome")

	// OutboxOldestAge is the age of the oldest unpublished outbox event.
	OutboxOldestAge = gauge("birge_outbox_oldest_unpublished_age_seconds",
		"Age of the oldest outbox event not yet published (0 when none).")
	// TimersDue is the number of timers due and not done.
	TimersDue = gauge("birge_timers_due",
		"Timers whose due time has passed and that are not done.")
	// TimersOldestOverdue is how long the oldest due timer has waited.
	TimersOldestOverdue = gauge("birge_timers_oldest_overdue_age_seconds",
		"How long the oldest due timer is overdue (0 when none).")
	// EscalationsUntaken is the number of open escalations nobody took.
	EscalationsUntaken = gauge("birge_escalations_untaken",
		"Open escalations not yet taken or closed (from the safety events in the outbox).")
	// EscalationsOldestUntaken is the age of the oldest untaken escalation.
	EscalationsOldestUntaken = gauge("birge_escalations_oldest_untaken_age_seconds",
		"Age of the oldest escalation not yet taken or closed (0 when none).")
	// SamplerLastSuccess is when the database gauges were last refreshed.
	SamplerLastSuccess = gauge("birge_sampler_last_success_timestamp_seconds",
		"Unix time of the last successful refresh of the database gauges.")

	// SMSGuardRefusals counts SMS the anti-pumping guard refused, by guard
	// ("notify", "signin") and reason (number_limit, global_limit,
	// daily_budget, store_error).
	SMSGuardRefusals = counterVec("birge_sms_guard_refusals_total",
		"SMS refused by the anti-pumping guard, by guard and reason.", "guard", "reason")
	// SMSGuardCounted counts SMS the guard counted against the daily budget.
	SMSGuardCounted = counterVec("birge_sms_guard_counted_total",
		"SMS counted against the daily budget, by guard.", "guard")
	// SMSGuardStoreErrors counts counter-store failures of the guard: an
	// ordinary SMS is then refused, a critical one still goes out.
	SMSGuardStoreErrors = counterVec("birge_sms_guard_store_errors_total",
		"SMS guard counter store failures, by guard.", "guard")
	// SMSGuardDailyBudget is the configured daily budget.
	SMSGuardDailyBudget = gaugeVec("birge_sms_guard_daily_budget",
		"Configured SMS budget per UTC day (0 means unlimited).", "guard")

	// CrisisPathErrors counts failures on the crisis path outside HTTP: the
	// escalation could not be opened, or the lexicon was missing.
	CrisisPathErrors = counterVec("birge_crisis_path_errors_total",
		"Crisis path failures by stage (escalation_open, lexicon_unavailable).", "stage")
	// LLMRequests counts assistant LLM calls by outcome (ok, error).
	LLMRequests = counterVec("birge_llm_requests_total",
		"Assistant LLM calls by outcome.", "outcome")
)

// smsDailyUsed serves birge_sms_guard_daily_used from functions, so the
// value is computed at scrape time and drops to 0 at UTC midnight even if
// no SMS has been sent since.
var smsDailyUsed = func() *funcGauges {
	g := &funcGauges{
		desc: prometheus.NewDesc("birge_sms_guard_daily_used",
			"SMS this process counted against the budget of the current UTC day, by guard; sum over replicas.",
			[]string{"guard"}, nil),
		fns: map[string]func() float64{},
	}
	factory(g)
	return g
}()

// SetSMSDailyUsed sets the function that reports guard's count for the
// current UTC day; a second call for the same guard replaces the first.
func SetSMSDailyUsed(guard string, fn func() float64) {
	smsDailyUsed.mu.Lock()
	defer smsDailyUsed.mu.Unlock()
	smsDailyUsed.fns[guard] = fn
}

// funcGauges is a gauge vector with one label whose values come from
// functions at collection time.
type funcGauges struct {
	desc *prometheus.Desc
	mu   sync.Mutex
	fns  map[string]func() float64
}

// Describe implements prometheus.Collector.
func (g *funcGauges) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

// Collect implements prometheus.Collector.
func (g *funcGauges) Collect(ch chan<- prometheus.Metric) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for label, fn := range g.fns {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, fn(), label)
	}
}

// ObserveHTTP records one request. route must be a route template.
func ObserveHTTP(route string, status int, elapsed time.Duration) {
	if !IsRouteTemplate(route) {
		route = "unmatched"
	}
	httpRequests.WithLabelValues(route, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(route).Observe(elapsed.Seconds())
}

// Handler serves Registry in the Prometheus text format. Mount it only on
// the internal metrics listener (BIRGE_METRICS_ADDR), never on a port the
// ingress routes.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}
