package e2e

import (
	"fmt"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seekin4u/preview-sweeper/test/utils"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting NamespaceSweeper real-cluster E2E test suite\n")
	RunSpecs(t, "NamespaceSweeper e2e suite")
}

var _ = BeforeSuite(func() {
	By("Checking connectivity to the cluster")
	cmd := exec.Command("kubectl", "cluster-info")
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "kubectl is not able to connect to the cluster")
	_, _ = fmt.Fprintf(GinkgoWriter, "Cluster info:\n%s\n", out)
})

var _ = AfterSuite(func() {
	// No cluster teardown here, tests done via k3s hetzner cloud
})
