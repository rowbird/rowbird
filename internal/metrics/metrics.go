// Package metrics exposes Prometheus metrics at /metrics (docs/spec/08-operations.md,
// "Observability"). Every instance has its own registry; nothing is registered globally. A nil
// *Metrics records nothing, so collaborators can take it as an optional dependency.
package metrics

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Source reads the values computed when Prometheus scrapes: they live in the store.
type Source interface {
	CountPendingRuns(ctx context.Context) (int, error)
	ArtifactBytes(ctx context.Context) (int64, error)
}

// scrapeTimeout bounds the store queries of one scrape.
const scrapeTimeout = 5 * time.Second

// Metrics holds the instance's collectors.
type Metrics struct {
	registry         *prometheus.Registry
	runs             *prometheus.CounterVec
	runDuration      prometheus.Histogram
	queryRows        prometheus.Histogram
	deliveries       *prometheus.CounterVec
	deliveryDuration *prometheus.HistogramVec
	schedulerLag     prometheus.Gauge
}

// New registers the metrics. version is reported by rowbird_build_info.
func New(src Source, version string, logger *slog.Logger) *Metrics {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rowbird_runs_total", Help: "Runs that finished, by status and trigger.",
		}, []string{"status", "trigger"}),
		runDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "rowbird_run_duration_seconds", Help: "Duration of finished runs, deliveries included.",
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
		}),
		queryRows: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "rowbird_query_rows", Help: "Rows returned by report queries.",
			Buckets: prometheus.ExponentialBuckets(1, 10, 7),
		}),
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rowbird_deliveries_total", Help: "Delivery sends, by destination and outcome (sent or failed).",
		}, []string{"destination", "status"}),
		deliveryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "rowbird_delivery_duration_seconds", Help: "Duration of delivery sends, retries included.",
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
		}, []string{"destination"}),
		schedulerLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rowbird_scheduler_lag_seconds", Help: "How late the scheduler queued the latest due run.",
		}),
	}
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rowbird_build_info", Help: "Always 1; the version is a label.", ConstLabels: prometheus.Labels{"version": version},
	})
	build.Set(1)
	m.registry.MustRegister(m.runs, m.runDuration, m.queryRows, m.deliveries, m.deliveryDuration, m.schedulerLag, build,
		&storeCollector{src: src, logger: logger},
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// RunFinished counts a finished run.
func (m *Metrics) RunFinished(status, trigger string, duration time.Duration, rows *int64) {
	if m == nil {
		return
	}
	m.runs.WithLabelValues(status, trigger).Inc()
	m.runDuration.Observe(duration.Seconds())
	if rows != nil {
		m.queryRows.Observe(float64(*rows))
	}
}

// DeliverySent counts a delivery send and its duration.
func (m *Metrics) DeliverySent(destination string, ok bool, duration time.Duration) {
	if m == nil {
		return
	}
	status := "sent"
	if !ok {
		status = "failed"
	}
	m.deliveries.WithLabelValues(destination, status).Inc()
	m.deliveryDuration.WithLabelValues(destination).Observe(duration.Seconds())
}

// SchedulerLag records how late the latest due run was queued.
func (m *Metrics) SchedulerLag(lag time.Duration) {
	if m == nil {
		return
	}
	m.schedulerLag.Set(max(lag, 0).Seconds())
}

// Handler serves the metrics. With a token, requests must send it as a bearer token.
func (m *Metrics) Handler(token string) http.Handler {
	h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	if token == "" {
		return h
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// storeCollector reads the queue and storage sizes from the store on each scrape.
type storeCollector struct {
	src    Source
	logger *slog.Logger
}

var (
	queueDesc   = prometheus.NewDesc("rowbird_queue_pending", "Runs waiting for a worker.", nil, nil)
	storageDesc = prometheus.NewDesc("rowbird_storage_bytes", "Bytes of stored artifacts.", nil, nil)
)

func (c *storeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueDesc
	ch <- storageDesc
}

func (c *storeCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	if n, err := c.src.CountPendingRuns(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(queueDesc, prometheus.GaugeValue, float64(n))
	} else {
		c.logger.WarnContext(ctx, "metrics: could not count pending runs", "error", err)
	}
	if n, err := c.src.ArtifactBytes(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(storageDesc, prometheus.GaugeValue, float64(n))
	} else {
		c.logger.WarnContext(ctx, "metrics: could not sum artifact sizes", "error", err)
	}
}
