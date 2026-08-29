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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sandglass/test/utils"
)

func testEphemeralDeployment() {
	const testNamespace = "default"
	const targetDeployName = "app-baseline"

	Context("EphemeralDeployment Operator Tests", func() {
		AfterEach(func() {
			By("cleaning up resources")
			utils.KubectlDelete("ephemeraldeployment", "--all", "-n", testNamespace)
			utils.KubectlDelete("deployment", targetDeployName, "-n", testNamespace)
			utils.KubectlDelete("service", targetDeployName, "-n", testNamespace)
			utils.KubectlDelete("httproute", "--all", "-n", testNamespace)
			utils.KubectlDelete("pod", "curl-dataplane", "-n", testNamespace)
		})

		It("should reject invalid EphemeralDeployment inputs", func() {
			By("rejecting missing targetDeployment")
			invalidYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: missing-target
  namespace: default
spec:
  routing:
    headerMatch: "test-env"
`
			err := utils.KubectlApply(invalidYAML)
			Expect(err).To(HaveOccurred(), "Expected creation to fail due to missing targetDeployment")

			By("rejecting invalid negative replicas")
			invalidYAML = `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: invalid-replicas
  namespace: default
spec:
  targetDeployment: "app-baseline"
  replicas: -1
`
			err = utils.KubectlApply(invalidYAML)
			Expect(err).To(HaveOccurred(), "Expected creation to fail due to negative replicas")

			By("rejecting invalid TTL format")
			invalidYAML = `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: invalid-ttl
  namespace: default
spec:
  targetDeployment: "app-baseline"
  ttl: "not-a-duration"
`
			err = utils.KubectlApply(invalidYAML)
			Expect(err).To(HaveOccurred(), "Expected creation to fail due to invalid TTL format")
		})

		It("should handle missing TargetDeployment gracefully", func() {
			By("creating EphemeralDeployment before baseline exists")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-missing
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			err := utils.KubectlApply(edYAML)
			Expect(err).NotTo(HaveOccurred())

			// Give it a moment to reconcile and mark Provisioning
			time.Sleep(5 * time.Second)

			By("applying baseline Deployment")
			err = utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("verifying ephemeral Deployment is eventually created")
			Eventually(func() error {
				return utils.WaitForDeploymentReady("test-missing", testNamespace, 1)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should properly route traffic based on headers and apply PodPatch", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying baseline HTTPRoute")
			baselineRoute := `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: baseline-route
  namespace: default
spec:
  parentRefs:
  - name: main-gateway
  rules:
  - backendRefs:
    - name: app-baseline
      port: 3000
`
			err = utils.KubectlApply(baselineRoute)
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR with PodPatch and custom routing")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-routing
  namespace: default
spec:
  targetDeployment: "app-baseline"
  routing:
    headerName: "X-Sandglass"
    headerMatch: "routing-test"
    gatewayRef: "main-gateway"
  podPatch:
    spec:
      containers:
      - name: web
        env:
        - name: INJECTED_VAR
          value: "injected-value"
`
			err = utils.KubectlApply(edYAML)
			Expect(err).NotTo(HaveOccurred())

			ephemeralName := "test-routing"

			By("verifying ephemeral Deployment is created and ready")
			Eventually(func() error {
				return utils.WaitForDeploymentReady(ephemeralName, testNamespace, 1)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying PodPatch was applied")
			out, err := utils.Kubectl("get", "deployment", ephemeralName, "-n", testNamespace, "-o", "jsonpath={.spec.template.spec.containers[0].env[0].name}")
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("INJECTED_VAR"))

			By("waiting for Envoy Gateway proxy to be provisioned")
			Eventually(func() error {
				out, err := utils.Kubectl("get", "svc", "-n", "envoy-gateway-system", "-l", "gateway.envoyproxy.io/owning-gateway-name=main-gateway", "-o", "jsonpath={.items[0].metadata.name}")
				if err != nil {
					return err
				}
				if strings.TrimSpace(out) == "" {
					return fmt.Errorf("envoy service not found")
				}
				return nil
			}, 3*time.Minute, 10*time.Second).Should(Succeed())

			envoySvcName, err := utils.Kubectl("get", "svc", "-n", "envoy-gateway-system", "-l", "gateway.envoyproxy.io/owning-gateway-name=main-gateway", "-o", "jsonpath={.items[0].metadata.name}")
			Expect(err).NotTo(HaveOccurred())
			envoySvcName = strings.TrimSpace(envoySvcName)

			By("testing baseline routing (NO header)")
			utils.KubectlDelete("pod", "curl-dataplane", "--namespace", testNamespace)
			_, err = utils.Kubectl("run", "curl-dataplane", "--restart=Never",
				"--namespace", testNamespace, "--image=curlimages/curl:latest", "--", "/bin/sh", "-c",
				fmt.Sprintf("for i in $(seq 1 30); do curl -f -s -v http://%s.envoy-gateway-system.svc.cluster.local:80/ && exit 0 || sleep 2; done; exit 1", envoySvcName))
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				phase, _ := utils.GetPodPhase("curl-dataplane", testNamespace)
				if phase != "Succeeded" {
					return fmt.Errorf("curl pod in wrong status: %s", phase)
				}
				return nil
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			logs, err := utils.Kubectl("logs", "curl-dataplane", "-n", testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(logs).To(ContainSubstring("Hostname: " + targetDeployName))

			By("testing ephemeral routing (WITH header)")
			utils.KubectlDelete("pod", "curl-dataplane", "--namespace", testNamespace)
			_, err = utils.Kubectl("run", "curl-dataplane", "--restart=Never",
				"--namespace", testNamespace, "--image=curlimages/curl:latest", "--", "/bin/sh", "-c",
				fmt.Sprintf("for i in $(seq 1 30); do curl -f -s -v -H 'X-Sandglass: routing-test' http://%s.envoy-gateway-system.svc.cluster.local:80/ && exit 0 || sleep 2; done; exit 1", envoySvcName))
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				phase, _ := utils.GetPodPhase("curl-dataplane", testNamespace)
				if phase != "Succeeded" {
					return fmt.Errorf("curl pod in wrong status: %s", phase)
				}
				return nil
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			logs, err = utils.Kubectl("logs", "curl-dataplane", "-n", testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(logs).To(ContainSubstring("Hostname: " + ephemeralName))
		})

		It("should delete the ephemeral environment when TTL expires", func() {
			By("applying baseline Deployment")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR with 10s TTL")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-ttl
  namespace: default
spec:
  targetDeployment: "app-baseline"
  ttl: "10s"
`
			err = utils.KubectlApply(edYAML)
			Expect(err).NotTo(HaveOccurred())

			By("verifying ExpiresAt is set in status")
			Eventually(func() error {
				out, err := utils.Kubectl("get", "ephemeraldeployment", "test-ttl", "-n", testNamespace, "-o", "jsonpath={.status.expiresAt}")
				if err != nil || strings.TrimSpace(out) == "" {
					return fmt.Errorf("expiresAt not set yet")
				}
				return nil
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("waiting for TTL to expire and resource to be deleted")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "ephemeraldeployment", "test-ttl", "-n", testNamespace)
				if err != nil && strings.Contains(err.Error(), "NotFound") {
					return nil
				}
				return fmt.Errorf("EphemeralDeployment still exists")
			}, 45*time.Second, 5*time.Second).Should(Succeed())

			By("verifying child Deployment is deleted")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "deployment", "test-ttl", "-n", testNamespace)
				if err != nil && strings.Contains(err.Error(), "NotFound") {
					return nil
				}
				return fmt.Errorf("child Deployment test-ttl still exists")
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should create all child resources and surface a Ready condition for a minimal CR", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying a minimal EphemeralDeployment CR (no TTL, no PodPatch, defaulting headerMatch to CR name)")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-minimal
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			err = utils.KubectlApply(edYAML)
			Expect(err).NotTo(HaveOccurred())

			ephemeralName := "test-minimal"

			By("verifying ephemeral Deployment is created and ready")
			Eventually(func() error {
				return utils.WaitForDeploymentReady(ephemeralName, testNamespace, 1)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying ephemeral Service exists")
			_, err = utils.Kubectl("get", "service", ephemeralName, "-n", testNamespace)
			Expect(err).NotTo(HaveOccurred())

			By("verifying HTTPRoute exists with correct header match (defaulting to CR name)")
			out, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace,
				"-o", "jsonpath={.spec.rules[0].matches[0].headers[0].value}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(Equal("test-minimal"))

			By("verifying status.phase is Ready")
			Eventually(func() string {
				phase, _ := utils.Kubectl("get", "ephemeraldeployment", "test-minimal", "-n", testNamespace,
					"-o", "jsonpath={.status.phase}")
				return strings.TrimSpace(phase)
			}, 30*time.Second, 2*time.Second).Should(Equal("Ready"))

			By("verifying status.active is true")
			active, err := utils.Kubectl("get", "ephemeraldeployment", "test-minimal", "-n", testNamespace,
				"-o", "jsonpath={.status.active}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(active)).To(Equal("true"))

			By("verifying Ready condition is set (enables kubectl wait)")
			Eventually(func() string {
				out, _ := utils.Kubectl("get", "ephemeraldeployment", "test-minimal", "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
				return strings.TrimSpace(out)
			}, 30*time.Second, 2*time.Second).Should(Equal("True"))

			By("verifying expiresAt is empty (no TTL set)")
			expiresAt, err := utils.Kubectl("get", "ephemeraldeployment", "test-minimal", "-n", testNamespace,
				"-o", "jsonpath={.status.expiresAt}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(expiresAt)).To(BeEmpty())

			By("verifying isolation labels on ephemeral pod template")
			envLabel, err := utils.Kubectl("get", "deployment", ephemeralName, "-n", testNamespace,
				"-o", "jsonpath={.spec.template.metadata.labels.app\\.kubernetes\\.io/env}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(envLabel)).To(Equal("ephemeral"))
		})

		It("should isolate multiple concurrent ephemeral environments from each other", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying two concurrent EphemeralDeployment CRs")
			for _, header := range []string{"env-alpha", "env-beta"} {
				yaml := fmt.Sprintf(`
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-%s
  namespace: default
spec:
  targetDeployment: "app-baseline"
  routing:
    headerMatch: "%s"
`, header, header)
				Expect(utils.KubectlApply(yaml)).To(Succeed())
			}

			By("waiting for both ephemeral Deployments to be ready")
			for _, header := range []string{"env-alpha", "env-beta"} {
				name := "test-" + header
				Eventually(func() error {
					return utils.WaitForDeploymentReady(name, testNamespace, 1)
				}, 2*time.Minute, 5*time.Second).Should(Succeed(), "deployment %s not ready", name)
			}

			By("verifying each Service selector only matches its own pods")
			for _, header := range []string{"env-alpha", "env-beta"} {
				name := "test-" + header
				svcSelector, err := utils.Kubectl("get", "service", name, "-n", testNamespace,
					"-o", "jsonpath={.spec.selector.app\\.kubernetes\\.io/name}")
				Expect(err).NotTo(HaveOccurred())
				Expect(strings.TrimSpace(svcSelector)).To(Equal(name),
					"Service %s selector mismatch", name)
			}

			By("verifying each HTTPRoute matches only its own header value")
			for _, header := range []string{"env-alpha", "env-beta"} {
				name := "test-" + header
				routeHeader, err := utils.Kubectl("get", "httproute", name, "-n", testNamespace,
					"-o", "jsonpath={.spec.rules[0].matches[0].headers[0].value}")
				Expect(err).NotTo(HaveOccurred())
				Expect(strings.TrimSpace(routeHeader)).To(Equal(header),
					"HTTPRoute %s header mismatch", name)
			}
		})

		It("should mirror exact ports from the baseline Service", func() {
			By("applying baseline (Service on port 3000 → targetPort 80)")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-ports
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			Expect(utils.KubectlApply(edYAML)).To(Succeed())

			ephemeralName := "test-ports"

			By("waiting for ephemeral Service to be created")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "service", ephemeralName, "-n", testNamespace)
				return err
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying Service port matches baseline Service port (3000)")
			port, err := utils.Kubectl("get", "service", ephemeralName, "-n", testNamespace,
				"-o", "jsonpath={.spec.ports[0].port}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(port)).To(Equal("3000"))

			By("verifying HTTPRoute backend port also uses 3000")
			routePort, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace,
				"-o", "jsonpath={.spec.rules[0].backendRefs[0].port}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(routePort)).To(Equal("3000"))
		})

		It("should use a custom gatewayRef from spec instead of the default", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR with explicit gatewayRef")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-gatewayref
  namespace: default
spec:
  targetDeployment: "app-baseline"
  routing:
    gatewayRef: "main-gateway"
`
			Expect(utils.KubectlApply(edYAML)).To(Succeed())

			ephemeralName := "test-gatewayref"

			By("waiting for HTTPRoute to be created")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace)
				return err
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying HTTPRoute parentRef name matches the specified gatewayRef")
			gwRef, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace,
				"-o", "jsonpath={.spec.parentRefs[0].name}")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(gwRef)).To(Equal("main-gateway"))
		})

		It("should cascade-delete all owned resources when the EphemeralDeployment CR is deleted", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-cascade
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			Expect(utils.KubectlApply(edYAML)).To(Succeed())

			ephemeralName := "test-cascade"

			By("waiting for child Deployment to be created")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "deployment", ephemeralName, "-n", testNamespace)
				return err
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			By("deleting the EphemeralDeployment CR")
			utils.KubectlDelete("ephemeraldeployment", "test-cascade", "-n", testNamespace)

			By("verifying child Deployment is garbage-collected")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "deployment", ephemeralName, "-n", testNamespace)
				if err != nil && strings.Contains(err.Error(), "NotFound") {
					return nil
				}
				return fmt.Errorf("child Deployment %s still exists", ephemeralName)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying child Service is garbage-collected")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "service", ephemeralName, "-n", testNamespace)
				if err != nil && strings.Contains(err.Error(), "NotFound") {
					return nil
				}
				return fmt.Errorf("child Service %s still exists", ephemeralName)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying child HTTPRoute is garbage-collected")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace)
				if err != nil && strings.Contains(err.Error(), "NotFound") {
					return nil
				}
				return fmt.Errorf("child HTTPRoute %s still exists", ephemeralName)
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should recreate the ephemeral Deployment when it is manually deleted", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-recreate-deploy
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			Expect(utils.KubectlApply(edYAML)).To(Succeed())

			ephemeralName := "test-recreate-deploy"

			By("waiting for ephemeral Deployment to be ready")
			Eventually(func() error {
				return utils.WaitForDeploymentReady(ephemeralName, testNamespace, 1)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("manually deleting the ephemeral Deployment")
			utils.KubectlDelete("deployment", ephemeralName, "-n", testNamespace)

			By("verifying the controller recreates the Deployment")
			Eventually(func() error {
				return utils.WaitForDeploymentReady(ephemeralName, testNamespace, 1)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should recreate the HTTPRoute when it is manually deleted", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			By("applying EphemeralDeployment CR")
			edYAML := `
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: test-recreate-route
  namespace: default
spec:
  targetDeployment: "app-baseline"
`
			Expect(utils.KubectlApply(edYAML)).To(Succeed())

			ephemeralName := "test-recreate-route"

			By("waiting for HTTPRoute to exist")
			Eventually(func() error {
				_, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace)
				return err
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			By("manually deleting the HTTPRoute")
			utils.KubectlDelete("httproute", ephemeralName, "-n", testNamespace)

			By("verifying the controller recreates the HTTPRoute with correct header match")
			Eventually(func() error {
				out, err := utils.Kubectl("get", "httproute", ephemeralName, "-n", testNamespace,
					"-o", "jsonpath={.spec.rules[0].matches[0].headers[0].value}")
				if err != nil {
					return err
				}
				if strings.TrimSpace(out) != "test-recreate-route" {
					return fmt.Errorf("HTTPRoute header mismatch: got %q", out)
				}
				return nil
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should remain stable and not crash after 5 consecutive create-delete cycles", func() {
			By("applying baseline Deployment and Service")
			err := utils.KubectlApplyFile("test/scripts/sample-app.yaml")
			Expect(err).NotTo(HaveOccurred())

			for i := range 5 {
				name := fmt.Sprintf("test-cycle-%02d", i)
				ephemeralName := name

				By(fmt.Sprintf("cycle %d: creating EphemeralDeployment", i))
				yaml := fmt.Sprintf(`
apiVersion: sandglass.io/v1alpha1
kind: EphemeralDeployment
metadata:
  name: %s
  namespace: default
spec:
  targetDeployment: "app-baseline"
`, name)
				Expect(utils.KubectlApply(yaml)).To(Succeed())

				By(fmt.Sprintf("cycle %d: waiting for Deployment to be ready", i))
				Eventually(func() error {
					return utils.WaitForDeploymentReady(ephemeralName, testNamespace, 1)
				}, 2*time.Minute, 5*time.Second).Should(Succeed())

				By(fmt.Sprintf("cycle %d: deleting EphemeralDeployment", i))
				utils.KubectlDelete("ephemeraldeployment", name, "-n", testNamespace)

				By(fmt.Sprintf("cycle %d: waiting for child Deployment deletion", i))
				Eventually(func() error {
					_, err := utils.Kubectl("get", "deployment", ephemeralName, "-n", testNamespace)
					if err != nil && strings.Contains(err.Error(), "NotFound") {
						return nil
					}
					return fmt.Errorf("deployment %s still exists", ephemeralName)
				}, 30*time.Second, 2*time.Second).Should(Succeed())
			}

			By("verifying controller pod is still running after all cycles")
			Eventually(func() string {
				podOutput, _ := utils.Kubectl("get", "pods",
					"-l", "control-plane=controller-manager",
					"-n", namespace,
					"-o", "jsonpath={.items[0].status.phase}")
				return strings.TrimSpace(podOutput)
			}, 30*time.Second, 2*time.Second).Should(Equal("Running"))
		})
	})
}
