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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	sandglassv1alpha1 "sandglass/api/v1alpha1"
)

var _ = Describe("EphemeralDeployment Controller", func() {
	const (
		resourceNamespace = "default"
	)

	ctx := context.Background()

	Context("When reconciling when baseline is not yet present", func() {
		const resourceName = "test-no-baseline"
		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			resource := &sandglassv1alpha1.EphemeralDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: resourceNamespace,
				},
				Spec: sandglassv1alpha1.EphemeralDeploymentSpec{
					TargetDeployment: "non-existent-baseline",
				},
			}
			_ = k8sClient.Create(ctx, resource)
		})

		AfterEach(func() {
			resource := &sandglassv1alpha1.EphemeralDeployment{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				_ = k8sClient.Delete(ctx, resource)
			}
		})

		It("should report Provisioning phase and BaselineNotFound condition", func() {
			reconciler := &EphemeralDeploymentReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(100),
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			updated := &sandglassv1alpha1.EphemeralDeployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Provisioning"))
			Expect(updated.Status.Active).To(BeFalse())
			Expect(updated.Status.Conditions).To(HaveLen(1))
			Expect(updated.Status.Conditions[0].Reason).To(Equal("BaselineNotFound"))
		})
	})

	Context("When baseline Deployment and Service exist", func() {
		const (
			baselineName  = "web-baseline"
			ephemeralName = "preview-pr-42"
		)

		baselineNN := types.NamespacedName{Name: baselineName, Namespace: resourceNamespace}
		ephemeralNN := types.NamespacedName{Name: ephemeralName, Namespace: resourceNamespace}

		BeforeEach(func() {
			// Create baseline deployment with 3 replicas
			three := int32(3)
			baselineDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      baselineName,
					Namespace: resourceNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &three,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "web"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "web"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "nginx:1.24",
									Ports: []corev1.ContainerPort{
										{Name: "http", ContainerPort: 80},
									},
								},
							},
						},
					},
				},
			}
			_ = k8sClient.Create(ctx, baselineDeploy)

			// Create baseline service
			baselineSvc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      baselineName,
					Namespace: resourceNamespace,
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": "web"},
					Ports: []corev1.ServicePort{
						{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP},
					},
				},
			}
			_ = k8sClient.Create(ctx, baselineSvc)

			// Create EphemeralDeployment with PodPatch and default routing
			ed := &sandglassv1alpha1.EphemeralDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ephemeralName,
					Namespace: resourceNamespace,
				},
				Spec: sandglassv1alpha1.EphemeralDeploymentSpec{
					TargetDeployment: baselineName,
					PodPatch: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "nginx:1.25-alpine",
									Env: []corev1.EnvVar{
										{Name: "FEATURE_FLAG", Value: "enabled"},
									},
								},
							},
						},
					},
				},
			}
			_ = k8sClient.Create(ctx, ed)
		})

		AfterEach(func() {
			ed := &sandglassv1alpha1.EphemeralDeployment{}
			if err := k8sClient.Get(ctx, ephemeralNN, ed); err == nil {
				_ = k8sClient.Delete(ctx, ed)
			}
			bd := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, baselineNN, bd); err == nil {
				_ = k8sClient.Delete(ctx, bd)
			}
			bs := &corev1.Service{}
			if err := k8sClient.Get(ctx, baselineNN, bs); err == nil {
				_ = k8sClient.Delete(ctx, bs)
			}
			cd := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, ephemeralNN, cd); err == nil {
				_ = k8sClient.Delete(ctx, cd)
			}
			cs := &corev1.Service{}
			if err := k8sClient.Get(ctx, ephemeralNN, cs); err == nil {
				_ = k8sClient.Delete(ctx, cs)
			}
			cr := &gatewayv1.HTTPRoute{}
			if err := k8sClient.Get(ctx, ephemeralNN, cr); err == nil {
				_ = k8sClient.Delete(ctx, cr)
			}
		})

		It("should clone baseline into child Deployment with single replica and patched image", func() {
			reconciler := &EphemeralDeploymentReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(100),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: ephemeralNN})
			Expect(err).NotTo(HaveOccurred())

			// 1. Verify child Deployment (named after CR: ADR 0004)
			childDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, ephemeralNN, childDeploy)).To(Succeed())
			Expect(childDeploy.Name).To(Equal(ephemeralName))
			// Replicas default to 1 (ADR 0005)
			Expect(*childDeploy.Spec.Replicas).To(Equal(int32(1)))
			// Selector is isolated
			Expect(childDeploy.Spec.Selector.MatchLabels).To(Equal(map[string]string{
				"app.kubernetes.io/env":  "ephemeral",
				"app.kubernetes.io/name": ephemeralName,
			}))
			// Patched image and env applied
			Expect(childDeploy.Spec.Template.Spec.Containers[0].Image).To(Equal("nginx:1.25-alpine"))
			Expect(childDeploy.Spec.Template.Spec.Containers[0].Env).To(ContainElement(
				corev1.EnvVar{Name: "FEATURE_FLAG", Value: "enabled"},
			))

			// 2. Verify child Service
			childSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, ephemeralNN, childSvc)).To(Succeed())
			Expect(childSvc.Name).To(Equal(ephemeralName))
			Expect(childSvc.Spec.Ports).To(HaveLen(1))
			Expect(childSvc.Spec.Ports[0].Port).To(Equal(int32(80)))

			// 3. Verify HTTPRoute with default X-Sandglass matching CR name (ADR 0001, ADR 0006)
			httpRoute := &gatewayv1.HTTPRoute{}
			Expect(k8sClient.Get(ctx, ephemeralNN, httpRoute)).To(Succeed())
			Expect(httpRoute.Name).To(Equal(ephemeralName))
			Expect(httpRoute.Spec.Rules).To(HaveLen(1))
			Expect(httpRoute.Spec.Rules[0].Matches[0].Headers[0].Name).To(Equal(gatewayv1.HTTPHeaderName("X-Sandglass")))
			Expect(httpRoute.Spec.Rules[0].Matches[0].Headers[0].Value).To(Equal(ephemeralName))
			Expect(string(httpRoute.Spec.Rules[0].BackendRefs[0].Name)).To(Equal(ephemeralName))
		})

		It("should snapshot the baseline and not alter child deployment when baseline changes", func() {
			reconciler := &EphemeralDeploymentReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(100),
			}

			// First reconcile creates the snapshot
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: ephemeralNN})
			Expect(err).NotTo(HaveOccurred())

			// Mutate the baseline deployment
			baselineDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, baselineNN, baselineDeploy)).To(Succeed())
			baselineDeploy.Spec.Template.Spec.Containers[0].Image = "nginx:1.99-drift"
			Expect(k8sClient.Update(ctx, baselineDeploy)).To(Succeed())

			// Second reconcile of ephemeral deployment
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: ephemeralNN})
			Expect(err).NotTo(HaveOccurred())

			// Child deployment image must remain the snapshotted patched image (nginx:1.25-alpine)
			childDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, ephemeralNN, childDeploy)).To(Succeed())
			Expect(childDeploy.Spec.Template.Spec.Containers[0].Image).To(Equal("nginx:1.25-alpine"))
		})
	})

	Context("When custom routing and TTL are specified", func() {
		const customResourceName = "preview-custom-routing"
		customNN := types.NamespacedName{Name: customResourceName, Namespace: resourceNamespace}
		baselineName := "custom-base"
		baselineNN := types.NamespacedName{Name: baselineName, Namespace: resourceNamespace}

		BeforeEach(func() {
			bd := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: baselineName, Namespace: resourceNamespace},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "custom"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "custom"}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "web", Image: "nginx:latest"}},
						},
					},
				},
			}
			_ = k8sClient.Create(ctx, bd)

			portVal := intstr.FromInt(8080)
			ed := &sandglassv1alpha1.EphemeralDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: customResourceName, Namespace: resourceNamespace},
				Spec: sandglassv1alpha1.EphemeralDeploymentSpec{
					TargetDeployment: baselineName,
					TTL:              &metav1.Duration{Duration: 2 * time.Hour},
					Routing: &sandglassv1alpha1.RoutingSpec{
						HeaderName:  "X-Custom-Env",
						HeaderMatch: "feature-branch-99",
						GatewayRef:  "custom-gateway",
						ServicePort: &portVal,
					},
				},
			}
			_ = k8sClient.Create(ctx, ed)
		})

		AfterEach(func() {
			ed := &sandglassv1alpha1.EphemeralDeployment{}
			if err := k8sClient.Get(ctx, customNN, ed); err == nil {
				_ = k8sClient.Delete(ctx, ed)
			}
			bd := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, baselineNN, bd); err == nil {
				_ = k8sClient.Delete(ctx, bd)
			}
			cd := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, customNN, cd); err == nil {
				_ = k8sClient.Delete(ctx, cd)
			}
			cs := &corev1.Service{}
			if err := k8sClient.Get(ctx, customNN, cs); err == nil {
				_ = k8sClient.Delete(ctx, cs)
			}
			cr := &gatewayv1.HTTPRoute{}
			if err := k8sClient.Get(ctx, customNN, cr); err == nil {
				_ = k8sClient.Delete(ctx, cr)
			}
		})

		It("should configure custom header matching and calculate status.expiresAt", func() {
			reconciler := &EphemeralDeploymentReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(100),
			}

			// First reconcile sets expiresAt
			res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: customNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Requeue).To(BeTrue()) // nolint:staticcheck

			// Second reconcile creates resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: customNN})
			Expect(err).NotTo(HaveOccurred())

			// Verify expiresAt
			updated := &sandglassv1alpha1.EphemeralDeployment{}
			Expect(k8sClient.Get(ctx, customNN, updated)).To(Succeed())
			Expect(updated.Status.ExpiresAt).NotTo(BeNil())
			Expect(updated.Status.ExpiresAt.After(time.Now())).To(BeTrue())

			// Verify HTTPRoute custom fields
			route := &gatewayv1.HTTPRoute{}
			Expect(k8sClient.Get(ctx, customNN, route)).To(Succeed())
			Expect(string(route.Spec.ParentRefs[0].Name)).To(Equal("custom-gateway"))
			Expect(route.Spec.Rules[0].Matches[0].Headers[0].Name).To(Equal(gatewayv1.HTTPHeaderName("X-Custom-Env")))
			Expect(route.Spec.Rules[0].Matches[0].Headers[0].Value).To(Equal("feature-branch-99"))
			Expect(*route.Spec.Rules[0].BackendRefs[0].Port).To(Equal(gatewayv1.PortNumber(8080)))
		})
	})
})
