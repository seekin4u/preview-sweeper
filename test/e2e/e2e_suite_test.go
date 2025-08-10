//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seekin4u/preview-sweeper/test/utils"
)

var _ = BeforeSuite(func() {
	By("Checking connectivity to the cluster")
	cmd := exec.Command("kubectl", "cluster-info")
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "kubectl is not able to connect to the cluster")
	_, _ = fmt.Fprintf(GinkgoWriter, "Cluster info:\n%s\n", out)
})
