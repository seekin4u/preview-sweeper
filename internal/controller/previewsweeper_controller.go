package controller

import (
	"context"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	sweepDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "preview_sweeper",
		Name:      "sweep_seconds",
		Help:      "Duration of a single sweep pass in seconds.",
		Buckets:   prometheus.DefBuckets,
	})
	sweepsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "preview_sweeper",
		Name:      "sweeps_total",
		Help:      "Total number of sweep passes executed.",
	})
	listErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "preview_sweeper",
		Name:      "list_errors_total",
		Help:      "Total number of errors when listing namespaces.",
	})
	// Per-sweep gauges (reset each pass)
	lastScanned = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "preview_sweeper",
		Name:      "last_sweep_namespaces_scanned",
		Help:      "Count of namespaces returned by the label selector in the last sweep.",
	})
	lastCandidates = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "preview_sweeper",
		Name:      "last_sweep_candidates",
		Help:      "Count of namespaces considered (label+prefix) in the last sweep.",
	})
	lastExpired = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "preview_sweeper",
		Name:      "last_sweep_expired",
		Help:      "Count of namespaces older than TTL in the last sweep.",
	})
	lastDeleted = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "preview_sweeper",
		Name:      "last_sweep_deleted",
		Help:      "Count of namespaces actually deleted in the last sweep.",
	})
	deletedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "preview_sweeper",
		Name:      "namespaces_deleted_total",
		Help:      "Total namespaces deletion outcomes.",
	}, []string{"result"}) // result=deleted|dry_run|error
)

const (
	LabelPreview   = "preview-sweeper.maxsauce.com/enabled"
	AnnotationTTL  = "preview-sweeper.maxsauce.com/ttl"
	AnnotationHold = "preview-sweeper.maxsauce.com/hold"
)

func init() {
	crmetrics.Registry.MustRegister(
		sweepDuration, sweepsTotal, listErrorsTotal,
		lastScanned, lastCandidates, lastExpired, lastDeleted,
		deletedTotal,
	)
}

type NamespaceSweeper struct {
	Client   client.Client
	TTL      time.Duration
	Recorder record.EventRecorder

	Interval      time.Duration
	JitterPercent float64 // optional: e.g., 0.05 = +-5% jitter; 0 disables it.

	DryRun bool
}

// Ensure NamespaceSweeper respects leader election.
var _ manager.LeaderElectionRunnable = (*NamespaceSweeper)(nil)

func (s *NamespaceSweeper) NeedLeaderElection() bool {
	return true
}

func (s *NamespaceSweeper) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("NamespaceSweeper")

	if s.Interval <= 0 {
		s.Interval = 24 * time.Hour
	}

	firstDelay := s.withJitter(s.Interval, 0.1)
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()

	logger.Info("Namespace sweeper started",
		"interval", s.Interval,
		"initialDelay", firstDelay,
		"jitterPercent", s.JitterPercent,
		"dryRun", s.DryRun,
	)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Namespace sweeper stopped")
			return nil
		case <-timer.C:
			s.SweepOnce(ctx)
			next := s.withJitter(s.Interval, s.JitterPercent)
			timer.Reset(next)
		}
	}
}

func (s *NamespaceSweeper) SweepOnce(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("NamespaceSweeper")
	start := time.Now()
	scanned := 0 // <-- add this

	var (
		candidates int
		expired    int
		deleted    int
	)
	// end-of-function metric updates
	defer func() {
		sweepsTotal.Inc()
		sweepDuration.Observe(time.Since(start).Seconds())
		logger.Info("Sweep finished",
			"scanned", scanned,
			"candidates", candidates,
			"expired", expired,
			"deleted", deleted,
			"took", time.Since(start),
		)
	}()

	sel := labels.SelectorFromSet(labels.Set{LabelPreview: "true"})
	listOpts := &client.ListOptions{LabelSelector: sel}

	var nsList corev1.NamespaceList
	if err := s.Client.List(ctx, &nsList, listOpts); err != nil {
		listErrorsTotal.Inc()
		logger.Error(err, "Failed to list namespaces")
		lastScanned.Set(0)
		lastCandidates.Set(0)
		lastExpired.Set(0)
		lastDeleted.Set(0)
		return
	}
	lastScanned.Set(float64(len(nsList.Items)))

	now := time.Now()

	for i := range nsList.Items {
		ns := &nsList.Items[i]
		if ns.DeletionTimestamp != nil {
			continue
		}

		if ns.Name == "kube-system" || ns.Name == "default" || ns.Name == "kube-public" {
			continue
		}

		if !strings.HasPrefix(ns.Name, "preview-") {
			continue
		}

		candidates++

		effectiveTTL, ttlSrc := resolveTTL(ns.Annotations, s.TTL)

		if ns.Annotations[AnnotationHold] == "true" {
			logger.Info("Skipping namespace (on-hold enabled)", "name", ns.Name, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			continue
		}

		if effectiveTTL <= 0 {
			logger.Info("Skipping namespace (non-positive TTL)", "name", ns.Name, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			continue
		}

		age := now.Sub(ns.CreationTimestamp.Time)
		if age <= effectiveTTL {
			continue
		}
		expired++

		if s.DryRun {
			deletedTotal.WithLabelValues("dry_run").Inc()
			logger.Info("[dry-run] Would delete expired namespace", "name", ns.Name, "age", age, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			if s.Recorder != nil {
				s.Recorder.Eventf(ns, corev1.EventTypeNormal, "NamespaceCleanupDryRun",
					"[dry-run] Would delete namespace %q: age %s exceeded TTL %s (%s)", ns.Name, age, effectiveTTL, ttlSrc)
			}
			continue
		}

		logger.Info("Deleting expired namespace", "name", ns.Name, "age", age, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
		if err := s.Client.Delete(ctx, ns); err != nil {
			deletedTotal.WithLabelValues("error").Inc()
			logger.Error(err, "Failed to delete namespace", "name", ns.Name)
			continue
		}
		deletedTotal.WithLabelValues("deleted").Inc()
		deleted++

		if s.Recorder != nil {
			s.Recorder.Eventf(ns, corev1.EventTypeNormal, "NamespaceCleanup",
				"Deleted namespace %q: age %s exceeded TTL %s (%s)", ns.Name, age, effectiveTTL, ttlSrc)
		}
	}

	// update gauges
	lastCandidates.Set(float64(candidates))
	lastExpired.Set(float64(expired))
	lastDeleted.Set(float64(deleted))
}

// annotation example: preview-sweeper.maxsauce.com/ttl="4h", "30m", "2h45m", "69" (int = hours)
func resolveTTL(annotations map[string]string, defaultTTL time.Duration) (time.Duration, string) {
	if annotations != nil {
		if raw, ok := annotations[AnnotationTTL]; ok {
			val := strings.TrimSpace(raw)
			if val != "" {
				if d, err := time.ParseDuration(val); err == nil {
					return d, "annotation"
				}
				if n, err := strconv.Atoi(val); err == nil {
					return time.Duration(n) * time.Hour, "annotation"
				}
			}
		}
	}
	return defaultTTL, "default"
}

// copied from the internets
func (s *NamespaceSweeper) withJitter(base time.Duration, pct float64) time.Duration {
	if pct <= 0 {
		return base
	}
	// simple, deterministic jitter based on current time (no rand needed)
	nanos := time.Now().UnixNano()
	sign := int64(1)
	if nanos&1 == 1 {
		sign = -1
	}
	delta := time.Duration(float64(base) * pct)
	return base + time.Duration(sign)*delta/2
}
