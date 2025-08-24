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
	// Optional TTL, supports time.ParseDuration formats (e.g., "2" (hours) "4h", "30m").
	AnnotationTTL = "preview-sweeper.maxsauce.com/ttl"
)

type NamespaceSweeper struct {
	Client   client.Client
	TTL      time.Duration
	Recorder record.EventRecorder
}

func (s *NamespaceSweeper) Start(ctx context.Context, interval time.Duration) {
	logger := log.FromContext(ctx)

	sel := labels.SelectorFromSet(labels.Set{LabelPreview: "true"})
	listOpts := &client.ListOptions{LabelSelector: sel}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("Namespace sweeper stopped")
				return

			case <-ticker.C:
				logger.Info("Starting namespace sweep")

				var nsList corev1.NamespaceList
				if err := s.Client.List(ctx, &nsList, listOpts); err != nil {
					logger.Error(err, "Failed to list namespaces")
					continue
				}

				now := time.Now()
				for i := range nsList.Items {
					ns := &nsList.Items[i]
					if ns.DeletionTimestamp != nil {
						continue
					}
					if !strings.HasPrefix(ns.Name, "preview-") {
						continue
					}

					//read TTL from annotation or use default if unset
					//default is passed from Helm upon creation
					effectiveTTL, ttlSrc := resolveTTL(ns.Annotations, s.TTL)

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
		}
	}()
}

// annotation example: preview-sweeper.maxsauce.com/ttl="4h", "30m", "2h45m", "69" (int = assuming hours).
// returns the TTL and a short string describing the source ("annotation" or "default") - good for logs.
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
