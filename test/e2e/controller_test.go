//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sandglass/test/utils"
)

// namespace where the project is deployed in
const namespace = "sandglass-system"

// serviceAccountName created for the project
const serviceAccountName = "sandglass-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "sandglass-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "sandglass-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		_, err := utils.Kubectl("create", "ns", namespace)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		_, err = utils.Kubectl("label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd := exec.Command("mise", "run", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("mise", "run", "deploy")
		cmd.Env = append(os.Environ(), fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		utils.KubectlDelete("pod", "curl-metrics", "-n", namespace)

		By("undeploying the controller-manager")
		cmd := exec.Command("mise", "run", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("mise", "run", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		utils.KubectlDelete("ns", namespace)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			controllerLogs, err := utils.Kubectl("logs", controllerPodName, "-n", namespace)
			if err == nil {
				GinkgoT().Logf("Controller logs:\n %s", controllerLogs)
			} else {
				GinkgoT().Logf("Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			eventsOutput, err := utils.Kubectl("get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			if err == nil {
				GinkgoT().Logf("Kubernetes events:\n%s", eventsOutput)
			} else {
				GinkgoT().Logf("Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			metricsOutput, err := utils.Kubectl("logs", "curl-metrics", "-n", namespace)
			if err == nil {
				GinkgoT().Logf("Metrics logs:\n %s", metricsOutput)
			} else {
				GinkgoT().Logf("Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			podDescription, err := utils.Kubectl("describe", "pod", controllerPodName, "-n", namespace)
			if err == nil {
				GinkgoT().Logf("Pod description:\n%s", podDescription)
			} else {
				GinkgoT().Logf("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				By("getting the name of the controller-manager pod")
				podOutput, err := utils.Kubectl("get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				By("validating the pod's status")
				phase, err := utils.GetPodPhase(controllerPodName, namespace)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(phase).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		// Test metrics
		testMetrics(&controllerPodName)

		// Test EphemeralDeployment reconciliation
		testEphemeralDeployment()
	})
})
