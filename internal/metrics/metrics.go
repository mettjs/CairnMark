// Package metrics owns the Prometheus instrumentation: the metric definitions
// and the /metrics exposition handler. Layers record observations through the
// functions here; nothing in this package depends on the rest of the system,
// so any layer may import it (the composition root wires GC observations in,
// keeping gc itself instrumentation-free).
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cairnmark_http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "pattern", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cairnmark_http_request_duration_seconds",
		Help:    "HTTP request latency, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "pattern"})

	gcSweeps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cairnmark_gc_sweeps_total",
		Help: "GC reconciliation sweeps, by result (ok | error).",
	}, []string{"result"})

	gcReclaimed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cairnmark_gc_reclaimed_total",
		Help: "Items reclaimed by GC, by kind (purged | orphans | expired_keys).",
	}, []string{"kind"})
)

// Handler serves the Prometheus exposition endpoint (GET /metrics).
func Handler() http.Handler { return promhttp.Handler() }

// ObserveRequest records one served HTTP request. pattern must be the matched
// route pattern, never the raw URL — path parameters like file ids would blow
// up label cardinality.
func ObserveRequest(method, pattern string, status int, elapsed time.Duration) {
	httpRequests.WithLabelValues(method, pattern, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(method, pattern).Observe(elapsed.Seconds())
}

// ObserveGCSweep records the outcome of one reconciliation sweep.
func ObserveGCSweep(purged, orphans, expiredKeys int, err error) {
	if err != nil {
		gcSweeps.WithLabelValues("error").Inc()
		return
	}
	gcSweeps.WithLabelValues("ok").Inc()
	gcReclaimed.WithLabelValues("purged").Add(float64(purged))
	gcReclaimed.WithLabelValues("orphans").Add(float64(orphans))
	gcReclaimed.WithLabelValues("expired_keys").Add(float64(expiredKeys))
}
