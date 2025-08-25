package controller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/seekin4u/preview-sweeper/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client
	k8sManager ctrl.Manager
	ctx        context.Context
	cancel     context.CancelFunc

	testTTL        = 3 * time.Second
	testSweepEvery = 500 * time.Millisecond
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Namespace Sweeper Suite")
}

var _ = BeforeSuite(func() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	crdPath := filepath.Join("..", "..", "config", "crd", "bases")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: false,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// scheme
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	Expect(err).NotTo(HaveOccurred())

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	ctx, cancel = context.WithCancel(context.Background())

	sw := &controller.NamespaceSweeper{
		Client:        k8sClient,
		TTL:           testTTL,
		Interval:      testSweepEvery,
		JitterPercent: 0, // deterministic in tests
	}
	Expect(k8sManager.Add(sw)).To(Succeed())

	// start manager in background
	go func() {
		defer GinkgoRecover()
		// expect k8s manager to be initialized and started
		Expect(k8sManager.Start(ctx)).To(Succeed())
	}()

	By("waiting for manager cache to sync")
	Eventually(func() bool {
		return k8sManager.GetCache().WaitForCacheSync(ctx)
	}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	Expect(testEnv.Stop()).To(Succeed())
	_ = os.Setenv("GINKGO_EDITOR_INTEGRATION", "true")
})
