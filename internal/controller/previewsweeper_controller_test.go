package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	labelPreview   = "preview-sweeper.maxsauce.com/enabled"
	annotationHold = "preview-sweeper.maxsauce.com/hold"
)

var _ = Describe("NamespaceSweeper", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		// Comfortable defaults for tiny TTL/interval in tests
		SetDefaultEventuallyTimeout(5 * time.Second)
		SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
		SetDefaultConsistentlyDuration(1 * time.Second)
		SetDefaultConsistentlyPollingInterval(100 * time.Millisecond)
	})

	It("deletes preview-* namespaces older than TTL (marks for deletion in envtest)", func() {
		ns := &corev1.Namespace{}
		ns.Name = "preview-old-1"
		ns.Labels = map[string]string{labelPreview: "true"}

		By("creating a preview namespace")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		By("waiting for the namespace to exceed TTL")
		time.Sleep(testTTL + 300*time.Millisecond)

		By("eventually observing it marked for deletion (envtest won’t fully remove it)")
		Eventually(func() bool {
			cur := &corev1.Namespace{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns.Name}, cur)
			if apierrors.IsNotFound(err) {
				// In a real cluster you may reach NotFound; treat as success.
				return true
			}
			Expect(err).NotTo(HaveOccurred())
			return cur.DeletionTimestamp != nil
		}).Should(BeTrue(), "expected preview namespace to be marked for deletion after TTL")
	})

	It("does NOT delete namespaces that don't match the preview-* prefix", func() {
		ns := &corev1.Namespace{}
		ns.Name = "prod-stable"
		// Intentionally NO preview label here.

		By("creating a non-preview namespace")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		// Let several sweeps run
		time.Sleep(testTTL + 2*testSweepEvery)

		By("ensuring it still exists and is not being deleted")
		Consistently(func() bool {
			cur := &corev1.Namespace{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns.Name}, cur)
			if apierrors.IsNotFound(err) {
				return false
			}
			Expect(err).NotTo(HaveOccurred())
			return cur.DeletionTimestamp == nil
		}).Should(BeTrue(), "non-preview namespace must not be deleted")
	})

	It("keeps young preview namespaces until they age past TTL, then marks them for deletion", func() {
		ns := &corev1.Namespace{}
		ns.Name = "preview-young"
		ns.Labels = map[string]string{labelPreview: "true"}

		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		By("ensuring it exists before TTL passes (no deletion timestamp)")
		Consistently(func() bool {
			cur := &corev1.Namespace{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns.Name}, cur)
			if apierrors.IsNotFound(err) {
				return false
			}
			Expect(err).NotTo(HaveOccurred())
			return cur.DeletionTimestamp == nil
		}).Should(BeTrue(), "preview namespace should not be deleted before TTL")

		By("waiting until it ages past TTL")
		time.Sleep(testTTL + 300*time.Millisecond)

		By("eventually observing deletion start (DeletionTimestamp set)")
		Eventually(func() bool {
			cur := &corev1.Namespace{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns.Name}, cur)
			if apierrors.IsNotFound(err) {
				return true // acceptable in real clusters
			}
			Expect(err).NotTo(HaveOccurred())
			return cur.DeletionTimestamp != nil
		}).Should(BeTrue(), "should be marked for deletion after TTL")
	})

	It("does NOT delete preview namespaces when hold annotation is true (even past TTL)", func() {
		ns := &corev1.Namespace{}
		ns.Name = "preview-held-1"
		ns.Labels = map[string]string{labelPreview: "true"}
		ns.Annotations = map[string]string{annotationHold: "true"}

		By("creating a held preview namespace")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		// Let it age past TTL and allow a couple of sweeps to run
		time.Sleep(testTTL + 2*testSweepEvery)

		By("ensuring it still exists and is not being deleted despite exceeding TTL")
		Consistently(func() bool {
			cur := &corev1.Namespace{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns.Name}, cur)
			if apierrors.IsNotFound(err) {
				return false
			}
			Expect(err).NotTo(HaveOccurred())
			return cur.DeletionTimestamp == nil
		}).Should(BeTrue(), "held preview namespace must not be deleted while hold=true")
	})
})
