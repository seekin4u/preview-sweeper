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
)

const (
	LabelPreview = "preview-sweeper.maxsauce.com/enabled"
	// Optional TTL, supports time.ParseDuration formats (e.g., "4h", "30m", "2h45m", or "69" (hours)).
	AnnotationTTL  = "preview-sweeper.maxsauce.com/ttl"
	AnnotationHold = "preview-sweeper.maxsauce.com/hold"
)

type NamespaceSweeper struct {
	Client   client.Client
	TTL      time.Duration
	Recorder record.EventRecorder

	Interval      time.Duration // required
	JitterPercent float64       // optional: e.g., 0.05 = +-5% jitter; 0 disables it.
}

func (s *NamespaceSweeper) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("NamespaceSweeper")

	if s.Interval <= 0 {
		s.Interval = 24 * time.Hour
	}

	//no math.rand() here
	firstDelay := s.withJitter(s.Interval, 0.1)
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()

	logger.Info("Namespace sweeper started", "interval", s.Interval, "initialDelay", firstDelay, "jitterPercent", s.JitterPercent)

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

	sel := labels.SelectorFromSet(labels.Set{LabelPreview: "true"})
	listOpts := &client.ListOptions{LabelSelector: sel}

	var nsList corev1.NamespaceList
	// we get labeled "sweeper/enabled=true" namespaces.
	if err := s.Client.List(ctx, &nsList, listOpts); err != nil {
		logger.Error(err, "Failed to list namespaces")
		return
	}

	now := time.Now()
	for i := range nsList.Items {
		ns := &nsList.Items[i]
		if ns.DeletionTimestamp != nil {
			continue
		}

		if ns.Name == "kube-system" || ns.Name == "default" || ns.Name == "kube-public" {
			continue
		}

		// from all labeled namespaces, we pick previews.
		if !strings.HasPrefix(ns.Name, "preview-") {
			continue
		}

		effectiveTTL, ttlSrc := resolveTTL(ns.Annotations, s.TTL)

		if ns.Annotations[AnnotationHold] == "true" {
			logger.Info("Skipping namespace (on-hold enabled)", "name", ns.Name, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			continue
		}

		// could be outdated, but left just cause i can.
		if effectiveTTL <= 0 {
			logger.Info("Skipping namespace (non-positive TTL)", "name", ns.Name, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			continue
		}

		age := now.Sub(ns.CreationTimestamp.Time)
		if age > effectiveTTL {
			logger.Info("Deleting expired namespace", "name", ns.Name, "age", age, "ttlSource", ttlSrc, "ttl", effectiveTTL.String())
			if err := s.Client.Delete(ctx, ns); err != nil {
				logger.Error(err, "Failed to delete namespace", "name", ns.Name)
			} else if s.Recorder != nil {
				s.Recorder.Eventf(ns, corev1.EventTypeNormal, "NamespaceCleanup",
					"Deleted namespace %q: age %s exceeded TTL %s (%s)", ns.Name, age, effectiveTTL, ttlSrc)
			}
		}
	}
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
