package matching

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds the Prometheus collectors for the matching pipeline.
// Construct once per service via NewMetrics; share across consumers.
type Metrics struct {
	MatchesWritten           *prometheus.CounterVec
	MatchScore               *prometheus.HistogramVec
	MatchLatency             *prometheus.HistogramVec
	DLQDepth                 *prometheus.GaugeVec
	RerankerPoolInUse        prometheus.Gauge
	CandidateMatchIndexStale *prometheus.CounterVec
}

// NewMetrics constructs the collectors against the given registerer.
// In tests pass prometheus.NewRegistry(); in production pass
// prometheus.DefaultRegisterer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		MatchesWritten: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "matches_written_total",
			Help: "Matches written, by path/kind/status.",
		}, []string{"path", "kind", "status"}),
		MatchScore: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "match_score_bucket",
			Help:    "Distribution of final match scores.",
			Buckets: prometheus.LinearBuckets(0, 0.05, 21),
		}, []string{"path", "kind"}),
		MatchLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "match_latency_seconds",
			Help:    "End-to-end latency per path/phase.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 14),
		}, []string{"path", "phase"}),
		DLQDepth: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "matching_dlq_depth",
			Help: "Current dead-letter depth, by subject.",
		}, []string{"subject"}),
		RerankerPoolInUse: factory.NewGauge(prometheus.GaugeOpts{
			Name: "reranker_pool_in_use",
			Help: "Reranker workers currently in flight.",
		}),
		CandidateMatchIndexStale: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "candidate_match_indexes_stale_total",
			Help: "Rebuilds triggered, by cause.",
		}, []string{"cause"}),
	}
}
