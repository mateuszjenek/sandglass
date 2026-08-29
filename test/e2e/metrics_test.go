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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sandglass/test/utils"
)

func testMetrics(controllerPodName *string) {
	It("should ensure the metrics endpoint is serving metrics", func() {
		By("creating a ClusterRoleBinding for the service account to allow access to metrics")
		utils.KubectlDelete("clusterrolebinding", metricsRoleBindingName)

		_, err := utils.Kubectl("create", "clusterrolebinding", metricsRoleBindingName,
			"--clusterrole=sandglass-metrics-reader",
			fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
		)
		Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

		By("validating that the metrics service is available")
		_, err = utils.Kubectl("get", "service", metricsServiceName, "-n", namespace)
		Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

		By("getting the service account token")
		token, err := serviceAccountToken()
		Expect(err).NotTo(HaveOccurred())
		Expect(token).NotTo(BeEmpty())

		By("ensuring the controller pod is ready")
		verifyControllerPodReady := func(g Gomega) {
			output, err := utils.Kubectl("get", "pod", *controllerPodName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "Controller pod not ready")
		}
		Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

		By("verifying that the controller manager is serving the metrics server")
		verifyMetricsServerStarted := func(g Gomega) {
			output, err := utils.Kubectl("logs", *controllerPodName, "-n", namespace)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("Serving metrics server"),
				"Metrics server not yet started")
		}
		Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

		// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

		By("creating the curl-metrics pod to access the metrics endpoint")
		_, err = utils.Kubectl("run", "curl-metrics", "--restart=Never",
			"--namespace", namespace,
			"--image=curlimages/curl:latest",
			"--overrides",
			curlMetricsPodOverrides(token, metricsServiceName, namespace, serviceAccountName))
		Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

		By("waiting for the curl-metrics pod to complete.")
		verifyCurlUp := func(g Gomega) {
			phase, err := utils.GetPodPhase("curl-metrics", namespace)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(phase).To(Equal("Succeeded"), "curl pod in wrong status")
		}
		Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

		By("getting the metrics by checking curl-metrics logs")
		verifyMetricsAvailable := func(g Gomega) {
			metricsOutput, err := getMetricsOutput()
			g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
			g.Expect(metricsOutput).NotTo(BeEmpty())
			g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
		}
		Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
	})
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	By("creating temporary file to store the token request")
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}
	defer os.Remove(tokenRequestFile)

	var out string
	verifyTokenCreation := func(g Gomega) {
		By("executing kubectl command to create the token")
		output, err := utils.Kubectl("create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		g.Expect(err).NotTo(HaveOccurred())

		By("parsing the JSON output to extract the token")
		var token tokenRequest
		err = json.Unmarshal([]byte(output), &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, nil
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	return utils.Kubectl("logs", "curl-metrics", "-n", namespace)
}

// curlMetricsPodOverrides generates the JSON overrides string for the curl-metrics pod.
func curlMetricsPodOverrides(token, svcName, ns, sa string) string {
	return fmt.Sprintf(`{
		"spec": {
			"containers": [{
				"name": "curl",
				"image": "curlimages/curl:latest",
				"command": ["/bin/sh", "-c"],
				"args": [
					"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
				],
				"securityContext": {
					"readOnlyRootFilesystem": true,
					"allowPrivilegeEscalation": false,
					"capabilities": {
						"drop": ["ALL"]
					},
					"runAsNonRoot": true,
					"runAsUser": 1000,
					"seccompProfile": {
						"type": "RuntimeDefault"
					}
				}
			}],
			"serviceAccountName": "%s"
		}
	}`, token, svcName, ns, sa)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
