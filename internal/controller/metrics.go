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

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricLabelNamespace        = "namespace"
	metricLabelTargetDeployment = "target_deployment"
	metricLabelReason           = "reason"
)

var (
	// EphemeralDeploymentsActive tracks the number of currently active EphemeralDeployments.
	EphemeralDeploymentsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sandglass_ephemeral_deployments_active",
			Help: "Current number of active EphemeralDeployments",
		},
		[]string{metricLabelNamespace, metricLabelTargetDeployment},
	)

	// EphemeralDeploymentsCreatedTotal tracks the total number of EphemeralDeployments created.
	EphemeralDeploymentsCreatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandglass_ephemeral_deployments_created_total",
			Help: "Total number of EphemeralDeployments created",
		},
		[]string{metricLabelNamespace, metricLabelTargetDeployment},
	)

	// EphemeralDeploymentsPurgedTotal tracks the total number of EphemeralDeployments purged.
	EphemeralDeploymentsPurgedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandglass_ephemeral_deployments_purged_total",
			Help: "Total number of EphemeralDeployments purged",
		},
		[]string{metricLabelNamespace, metricLabelTargetDeployment, metricLabelReason},
	)

	// TTLDurationSeconds tracks the distribution of configured TTL durations in seconds.
	TTLDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sandglass_ttl_duration_seconds",
			Help:    "Distribution of EphemeralDeployment TTL durations in seconds",
			Buckets: []float64{300, 900, 1800, 3600, 7200, 14400, 28800, 86400},
		},
		[]string{metricLabelNamespace, metricLabelTargetDeployment},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		EphemeralDeploymentsActive,
		EphemeralDeploymentsCreatedTotal,
		EphemeralDeploymentsPurgedTotal,
		TTLDurationSeconds,
	)
}
