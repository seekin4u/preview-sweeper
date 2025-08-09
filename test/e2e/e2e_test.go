package e2e

import (
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seekin4u/preview-sweeper/test/utils"
)

const (
	namespace = "preview-sweeper-system"
	testNS    = "preview-test"
)

var _ = Describe("NamespaceSweeper in non-local cluster", Ordered, func() {
	BeforeAll(func() {
		By("deploying the controller-manager into the cluster")
		cmd := exec.Command("make", "deploy", "IMG=example.com/preview-sweeper:v0.0.1")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller")

		Eventually(func() string {
			out, _ := utils.Run(exec.Command(
				"kubectl", "get", "pods",
				"-n", namespace,
				"-l", "control-plane=controller-manager",
				"-o", "jsonpath={.items[0].status.phase}",
			))
			return out
		}, 2*time.Minute, 5*time.Second).Should(Equal("Running"))
	})

	AfterAll(func() {
		By("undeploying the controller-manager")
		cmd := exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)
	})

	It("should delete preview-* namespaces older than TTL", func() {
		By("creating a preview namespace")
		cmd := exec.Command("kubectl", "create", "namespace", testNS)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for namespace to be deleted after TTL")
		Eventually(func() bool {
			_, err := utils.Run(exec.Command(
				"kubectl", "get", "namespace", testNS,
			))
			return err != nil
		}, 3*time.Minute, 10*time.Second).Should(BeTrue(), "Namespace should be deleted by sweeper")
	})

	It("should NOT delete non-preview namespaces", func() {
		nonPreview := "prod-stable"
		By("creating a non-preview namespace")
		cmd := exec.Command("kubectl", "create", "namespace", nonPreview)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for a while to ensure it's not deleted")
		Consistently(func() bool {
			_, err := utils.Run(exec.Command(
				"kubectl", "get", "namespace", nonPreview,
			))
			return err == nil
		}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Non-preview namespace should persist")

		_ = exec.Command("kubectl", "delete", "namespace", nonPreview).Run()
	})
})
