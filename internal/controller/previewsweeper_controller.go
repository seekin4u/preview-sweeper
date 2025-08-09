/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type NamespaceSweeper struct {
	Client   client.Client
	TTL      time.Duration
	Recorder record.EventRecorder
}

func (s *NamespaceSweeper) Start(ctx context.Context, interval time.Duration) {
	logger := log.FromContext(ctx)

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
				if err := s.Client.List(ctx, &nsList); err != nil {
					logger.Error(err, "Failed to list namespaces")
					continue
				}

				now := time.Now()
				for _, ns := range nsList.Items {
					if strings.HasPrefix(ns.Name, "preview-") {
						age := now.Sub(ns.CreationTimestamp.Time)
						if age > s.TTL {
							logger.Info("Deleting expired namespace", "name", ns.Name, "age", age)
							if err := s.Client.Delete(ctx, &ns); err != nil {
								logger.Error(err, "Failed to delete namespace", "name", ns.Name)
							} else if s.Recorder != nil {
								s.Recorder.Eventf(&ns, corev1.EventTypeNormal, "NamespaceCleanup",
									"Deleted namespace %q, older than %s", ns.Name, s.TTL)
							}
						}
					}
				}
			}
		}
	}()
}
