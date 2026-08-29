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

package utils

import (
	"fmt"
	"os/exec"
	"strings"
)

// Kubectl executes a kubectl command with the given arguments and returns its output.
func Kubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	return Run(cmd)
}

// KubectlApply applies the given YAML manifest string via kubectl stdin.
func KubectlApply(manifest string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := Run(cmd)
	return err
}

// KubectlApplyFile applies the given YAML file path via kubectl.
func KubectlApplyFile(path string) error {
	_, err := Kubectl("apply", "-f", path)
	return err
}

// KubectlDelete performs a best-effort kubectl delete, ignoring errors.
// Intended for cleanup where failure is not critical.
func KubectlDelete(args ...string) {
	fullArgs := append([]string{"delete"}, args...)
	_, _ = Kubectl(fullArgs...)
}

// GetPodPhase returns the phase of the named pod in the given namespace.
func GetPodPhase(name, namespace string) (string, error) {
	output, err := Kubectl("get", "pod", name, "-n", namespace, "-o", "jsonpath={.status.phase}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// WaitForDeploymentReady polls until the named deployment has the expected number of ready replicas.
func WaitForDeploymentReady(name, namespace string, replicas int) error {
	expected := fmt.Sprintf("%d", replicas)
	output, err := Kubectl("get", "deployment", name, "-n", namespace, "-o", "jsonpath={.status.readyReplicas}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != expected {
		return fmt.Errorf("deployment %s: got %s ready replicas, want %s", name, output, expected)
	}
	return nil
}
