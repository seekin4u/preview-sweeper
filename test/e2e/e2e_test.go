package e2e

import (
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seekin4u/preview-sweeper/test/utils"
)

const namespace = "preview-sweeper-system"
const serviceAccountName = "preview-sweeper-controller-manager"
const metricsServiceName = "preview-sweeper-controller-manager-metrics-service"
const metricsRoleBindingName = "preview-sweeper-metrics-binding"
const projectImage = "ghcr.io/seekin4u/preview-sweeper:v0.0.2"

var _ = Describe("NamespaceSweeper in non-local cluster", Ordered, func() {
	BeforeAll(func() {
		By("deploying the controller-manager into the cluster")
		cmd := exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("patching deployment to use short TTL and sweep interval for tests")
		patch := `[
			{"op":"replace","path":"/spec/template/spec/containers/0/args","value":[
				"--metrics-bind-address=:8443",
				"--health-probe-bind-address=:8081",
				"--leader-elect=false",
				"--sweep-every=5s",
				"--ttl=10s"
			]}
		]`
		cmd = exec.Command("kubectl", "-n", namespace, "patch", "deploy", "preview-sweeper-controller-manager",
			"--type=json", "-p", patch)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to patch controller deployment")

		By("waiting for patched controller to be ready")
		cmd = exec.Command("kubectl", "-n", namespace, "rollout", "status", "deploy", "preview-sweeper-controller-manager")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Controller rollout failed")
	})

	AfterAll(func() {
		By("undeploying the controller-manager")
		cmd := exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	It("should delete preview-* namespaces older than TTL", func() {
		const testNS = "preview-test"

		// Ensure previous test NS is gone before creating
		By(fmt.Sprintf("cleaning up old namespace %s if it exists", testNS))
		_ = exec.Command("kubectl", "delete", "namespace", testNS, "--ignore-not-found=true").Run()

		By("creating a preview namespace")
		cmd := exec.Command("kubectl", "create", "namespace", testNS)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("waiting for namespace to be deleted after TTL")
		Eventually(func() bool {
			cmd := exec.Command("kubectl", "get", "namespace", testNS)
			_, err := utils.Run(cmd)
			return err != nil // namespace gone if err != nil
		}).Should(BeTrue(), "Namespace should be deleted by sweeper")
	})
})
