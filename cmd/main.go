package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/seekin4u/preview-sweeper/internal/controller"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Defaults from flag definitions
const (
	defaultSweepEvery = 24 * time.Hour
	defaultTTL        = 72 * time.Hour
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)

	var sweepEvery time.Duration
	var ttl time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "Metrics bind address, use 0 to disable")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve metrics securely via HTTPS")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "Path to webhook cert directory")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "Webhook cert filename")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "Webhook key filename")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "", "Path to metrics cert directory")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "Metrics cert filename")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "Metrics key filename")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for metrics/webhooks")

	// Sweeper flags
	flag.DurationVar(&sweepEvery, "sweep-every", defaultSweepEvery, "How often to sweep namespaces")
	flag.DurationVar(&ttl, "ttl", defaultTTL, "Namespace TTL before deletion")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Allow overriding from environment variables (useful for tests)
	if envVal := os.Getenv("SWEEP_EVERY"); envVal != "" {
		if dur, err := time.ParseDuration(envVal); err == nil {
			sweepEvery = dur
		}
	}
	if envVal := os.Getenv("TTL"); envVal != "" {
		if dur, err := time.ParseDuration(envVal); err == nil {
			ttl = dur
		}
	}

	// Sanity checks
	if ttl <= 0 {
		setupLog.Info("TTL was <= 0, setting to default", "defaultTTL", defaultTTL)
		ttl = defaultTTL
	}
	if sweepEvery <= 0 {
		setupLog.Info("SweepEvery was <= 0, setting to default", "defaultSweepEvery", defaultSweepEvery)
		sweepEvery = defaultSweepEvery
	}

	setupLog.Info("Configuration parsed",
		"SweepEvery", sweepEvery,
		"TTL", ttl,
		"MetricsAddr", metricsAddr,
		"LeaderElect", enableLeaderElection,
	)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// HTTP/2 disable for security unless explicitly enabled
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			setupLog.Info("Disabling HTTP/2 for metrics/webhooks")
			c.NextProtos = []string{"http/1.1"}
		})
	}

	// Cert watchers
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher
	var err error

	if len(webhookCertPath) > 0 {
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to init webhook cert watcher")
			os.Exit(1)
		}
	}

	if len(metricsCertPath) > 0 {
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to init metrics cert watcher")
			os.Exit(1)
		}
	}

	webhookTLSOpts := tlsOpts
	if webhookCertWatcher != nil {
		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}
	webhookServer := webhook.NewServer(webhook.Options{TLSOpts: webhookTLSOpts})

	// Metrics server setup
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}
	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if metricsCertWatcher != nil {
		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	// Manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "8a12db1b.maxsauce.com",
	})
	if err != nil {
		setupLog.Error(err, "Unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	rec := mgr.GetEventRecorderFor("preview-sweeper")
	sweeper := &controller.NamespaceSweeper{
		Client:   mgr.GetClient(),
		TTL:      ttl,
		Recorder: rec,
	}
	sweeper.Start(ctx, sweepEvery)

	if metricsCertWatcher != nil {
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add metrics cert watcher")
			os.Exit(1)
		}
	}
	if webhookCertWatcher != nil {
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add webhook cert watcher")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info(fmt.Sprintf(
		"Starting manager: SweepEvery(%s), TTL(%s)",
		sweepEvery, ttl,
	))
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Problem running manager")
		os.Exit(1)
	}
}
