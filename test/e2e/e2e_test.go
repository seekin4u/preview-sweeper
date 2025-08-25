//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seekin4u/preview-sweeper/test/utils"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	fmt.Fprintln(GinkgoWriter, "Starting NamespaceSweeper real-cluster E2E test suite")
	RunSpecs(t, "NamespaceSweeper e2e suite")
}

var _ = Describe("NamespaceSweeper in non-local cluster", Ordered, func() {
	var (
		img             string
		ctrlNS          string
		testNS          string
		testNSHold      string
		crName          string
		crbName         string
		deployName      = "preview-sweeper-controller-manager"
		sweepEvery      = "5s"
		ttl             = "10s"
		previewLabelKey = "preview-sweeper.maxsauce.com/enabled"
	)

	BeforeAll(func() {
		img = os.Getenv("E2E_IMG")
		if img == "" {
			img = "ghcr.io/seekin4u/preview-sweeper:v0.0.3"
		}

		suffix := time.Now().Unix()
		ctrlNS = fmt.Sprintf("sweeper-e2e-%d", suffix)
		testNS = fmt.Sprintf("preview-test-%d", suffix)
		testNSHold = fmt.Sprintf("preview-held-%d", suffix)
		crName = fmt.Sprintf("preview-sweeper-e2e-%d", suffix)
		crbName = fmt.Sprintf("preview-sweeper-e2e-%d", suffix)

		By("creating a dedicated controller namespace")
		_, err := utils.Run(exec.Command("kubectl", "create", "namespace", ctrlNS))
		Expect(err).NotTo(HaveOccurred(), "failed to create controller namespace")

		By("applying minimal RBAC and controller Deployment")
		yaml := minimalBundleYAML(ctrlNS, crName, crbName, deployName, img, sweepEvery, ttl)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(yaml)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "failed to apply controller bundle")

		By("waiting for controller Deployment to become Available")
		_, err = utils.Run(exec.Command(
			"kubectl", "-n", ctrlNS, "rollout", "status",
			"deploy/"+deployName, "--timeout=180s",
		))
		Expect(err).NotTo(HaveOccurred(), "controller rollout failed")

		// some waiting here
		By("waiting for controller Pod to be Ready")
		_, err = utils.Run(exec.Command(
			"kubectl", "-n", ctrlNS, "wait", "--for=condition=Ready",
			"pod", "-l", "app=preview-sweeper,control-plane=preview-sweeper-controller",
			"--timeout=120s",
		))
		Expect(err).NotTo(HaveOccurred(), "controller pod not ready")
	})

	AfterAll(func() {
		By("cleaning test namespace if present")
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", testNS, "--ignore-not-found=true", "--wait=false"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", testNSHold, "--ignore-not-found=true", "--wait=false"))

		By("cleaning controller namespace and RBAC")
		_, _ = utils.Run(exec.Command("kubectl", "delete", "deploy", deployName, "-n", ctrlNS, "--ignore-not-found=true", "--wait=false"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "sa", "controller-sa", "-n", ctrlNS, "--ignore-not-found=true"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", ctrlNS, "--ignore-not-found=true", "--wait=false"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "clusterrole", crName, "--ignore-not-found=true"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "clusterrolebinding", crbName, "--ignore-not-found=true"))
	})

	SetDefaultEventuallyTimeout(3 * time.Minute)
	SetDefaultEventuallyPollingInterval(5 * time.Second)

	It("should delete preview-* namespaces older than TTL", func() {
		By("creating a preview-* namespace")
		_, err := utils.Run(exec.Command("kubectl", "create", "namespace", testNS))
		Expect(err).NotTo(HaveOccurred(), "failed to create preview test namespace")

		// label or NS will be skipped straight outa
		By("labeling the namespace to enable sweeping")
		_, err = utils.Run(exec.Command(
			"kubectl", "label", "namespace", testNS,
			fmt.Sprintf("%s=true", previewLabelKey),
			"--overwrite",
		))
		Expect(err).NotTo(HaveOccurred(), "failed to label preview test namespace")

		By("waiting for the sweeper to delete or mark it for deletion")
		Eventually(func() bool {
			// If NotFound -> success
			out, err := utils.Run(exec.Command(
				"kubectl", "get", "namespace", testNS,
				"-o", "jsonpath={.metadata.deletionTimestamp}",
			))
			if err != nil {
				// deleted
				return true
			}
			// Or deletionTimestamp is set
			return strings.TrimSpace(out) != ""
		}).Should(BeTrue(), "namespace should be deleted or marked for deletion by sweeper")
	})

	It("honors hold annotation: does not delete while hold=true, then deletes after hold is removed", func() {
		holdKey := "preview-sweeper.maxsauce.com/hold"

		By("creating a preview-* namespace to be held")
		_, err := utils.Run(exec.Command("kubectl", "create", "namespace", testNSHold))
		Expect(err).NotTo(HaveOccurred(), "failed to create held preview namespace")

		By("labeling the namespace to enable sweeping")
		_, err = utils.Run(exec.Command(
			"kubectl", "label", "namespace", testNSHold,
			fmt.Sprintf("%s=true", previewLabelKey),
			"--overwrite",
		))
		Expect(err).NotTo(HaveOccurred(), "failed to label held preview namespace")

		By("annotating hold=true to prevent deletion")
		_, err = utils.Run(exec.Command(
			"kubectl", "annotate", "namespace", testNSHold,
			fmt.Sprintf("%s=true", holdKey),
			"--overwrite",
		))
		Expect(err).NotTo(HaveOccurred(), "failed to annotate hold=true")

		// let this mofo to age
		parsedTTL, _ := time.ParseDuration(ttl)
		parsedSweep, _ := time.ParseDuration(sweepEvery)
		time.Sleep(parsedTTL + 2*parsedSweep)

		By("verifying it is NOT marked for deletion while hold=true")
		Consistently(func() bool {
			out, err := utils.Run(exec.Command(
				"kubectl", "get", "namespace", testNSHold,
				"-o", "jsonpath={.metadata.deletionTimestamp}",
			))
			if err != nil {
				// NotFound would mean hold aint working
				return false
			}
			// deletionTimestamp must remain empty while held=true
			return strings.TrimSpace(out) == ""
		}, 30*time.Second, 5*time.Second).Should(BeTrue(), "held namespace must not be deleted or marked for deletion")

		By("removing the hold annotation")
		// Kubernetes convention: <key>- removes the annotation
		_, err = utils.Run(exec.Command(
			"kubectl", "annotate", "namespace", testNSHold,
			fmt.Sprintf("%s-", holdKey),
		))
		Expect(err).NotTo(HaveOccurred(), "failed to remove hold annotation")

		By("eventually observing deletion once hold is removed (either NotFound or deletionTimestamp set)")
		Eventually(func() bool {
			out, err := utils.Run(exec.Command(
				"kubectl", "get", "namespace", testNSHold,
				"-o", "jsonpath={.metadata.deletionTimestamp}",
			))
			if err != nil {
				// already deleted in this case
				return true
			}
			return strings.TrimSpace(out) != ""
		}).Should(BeTrue(), "namespace should be deleted after hold is removed and TTL already exceeded")
	})
})

// minimalBundleYAML returns a tiny self-contained manifest bundle:
// - ServiceAccount in ctrlNS
// - ClusterRole with list/get/delete namespaces + events write
// - ClusterRoleBinding SA->CR
// - Deployment running your image with flags for short sweep/ttl
func minimalBundleYAML(
	ctrlNS, crName, crbName, deployName, image, sweepEvery, ttl string,
) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: controller-sa
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %s
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get","list","delete"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create","patch","update"]
  - apiGroups: ["events.k8s.io"]
    resources: ["events"]
    verbs: ["create","patch","update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: %s
subjects:
  - kind: ServiceAccount
    name: controller-sa
    namespace: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: preview-sweeper
    control-plane: preview-sweeper-controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: preview-sweeper
      control-plane: preview-sweeper-controller
  template:
    metadata:
      labels:
        app: preview-sweeper
        control-plane: preview-sweeper-controller
    spec:
      serviceAccountName: controller-sa
      containers:
        - name: manager
          image: %s
          imagePullPolicy: IfNotPresent
          args:
            - --metrics-bind-address=0
            - --health-probe-bind-address=:8081
            - --leader-elect=false
            - --sweep-every=%s
            - --ttl=%s
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8081
            initialDelaySeconds: 2
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8081
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              cpu: "20m"
              memory: "64Mi"
            limits:
              cpu: "200m"
              memory: "256Mi"
`, ctrlNS, crName, crbName, crName, ctrlNS, deployName, ctrlNS, image, sweepEvery, ttl)
}
