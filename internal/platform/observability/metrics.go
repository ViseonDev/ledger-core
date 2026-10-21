// Package observability holds the service's metrics and logging setup.
package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds every collector this service exposes.
type Metrics struct {
	registry *prometheus.Registry

	HTTPRequests   *prometheus.CounterVec
	HTTPDuration   *prometheus.HistogramVec
	PostingResults *prometheus.CounterVec
	PostedAmount   *prometheus.CounterVec
}

// NewMetrics builds and registers the collectors.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,

		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_http_requests_total",
			Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),

		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ledger_http_request_duration_seconds",
			Help: "HTTP request duration by route.",
			// Smaller buckets than the default, postings take a few ms.
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}, //nolint:forbidigo
		}, []string{"method", "route"}),

		PostingResults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_postings_total",
			Help: "Transaction postings by outcome.",
		}, []string{"outcome"}),

		PostedAmount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_posted_minor_units_total",
			Help: "Total value posted, in currency minor units.",
		}, []string{"currency"}),
	}

	registry.MustRegister(
		m.HTTPRequests, m.HTTPDuration, m.PostingResults, m.PostedAmount,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// ObserveHTTP records one completed request.
func (m *Metrics) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	m.HTTPRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.HTTPDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())
}

// ObservePosting records the outcome of a transaction posting.
func (m *Metrics) ObservePosting(outcome string) {
	m.PostingResults.WithLabelValues(outcome).Inc()
}

// ObservePostedAmount records value moved.
func (m *Metrics) ObservePostedAmount(currency string, minorUnits int64) {
	// Prometheus needs float64. Only used for monitoring, never for accounting.
	m.PostedAmount.WithLabelValues(currency).Add(float64(minorUnits)) //nolint:forbidigo
}
