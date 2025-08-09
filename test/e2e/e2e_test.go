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
		img        string
		ctrlNS     string
		testNS     string
		crName     string
		crbName    string
		deployName = "preview-sweeper-controller-manager"
		sweepEvery = "5s"
		ttl        = "10s"
	)

	BeforeAll(func() {
		img = os.Getenv("E2E_IMG")
		if img == "" {
			img = "ghcr.io/seekin4u/preview-sweeper:v0.0.3"
		}

		suffix := time.Now().Unix()
		ctrlNS = fmt.Sprintf("sweeper-e2e-%d", suffix)
		testNS = fmt.Sprintf("preview-test-%d", suffix)
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
	})

	AfterAll(func() {
		By("cleaning test namespace if present")
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", testNS, "--ignore-not-found=true", "--wait=false"))

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
    control-plane: controller-manager
spec:
  replicas: 1
  selector:
    matchLabels:
      app: preview-sweeper
      control-plane: controller-manager
  template:
    metadata:
      labels:
        app: preview-sweeper
        control-plane: controller-manager
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
