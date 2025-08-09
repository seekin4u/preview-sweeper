//go:build e2e

package e2e

import (
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = BeforeSuite(func() {
	// Fast, fails if the apiserver is unreachable
	cmd := exec.Command("kubectl", "version", "--request-timeout=5s")
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "cannot talk to cluster: %s", string(out))
})
