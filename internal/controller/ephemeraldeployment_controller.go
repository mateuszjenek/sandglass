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
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	sandglassv1alpha1 "sandglass/api/v1alpha1"
)

const (
	labelAppEnv   = "app.kubernetes.io/env"
	labelAppName  = "app.kubernetes.io/name"
	envEphemeral  = "ephemeral"
	portNameHTTP  = "http"
	portNameWeb   = "web"
	portNameHTTPS = "https"
	defaultPort80 = 80
)

// EphemeralDeploymentReconciler reconciles a EphemeralDeployment object
type EphemeralDeploymentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=sandglass.io,resources=ephemeraldeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sandglass.io,resources=ephemeraldeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sandglass.io,resources=ephemeraldeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *EphemeralDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ephemeral sandglassv1alpha1.EphemeralDeployment
	if err := r.Get(ctx, req.NamespacedName, &ephemeral); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Unable to fetch EphemeralDeployment")
		return ctrl.Result{}, err
	}

	headerName, headerMatch, gatewayRef, servicePort := getRoutingDefaults(&ephemeral)
	desiredReplicas := getDesiredReplicas(&ephemeral)
	childName := ephemeral.Name

	// Handle TTL initialization, expiry, and purging
	ttlResult, done, err := r.handleTTL(ctx, &ephemeral)
	if err != nil || done {
		return ttlResult, err
	}

	// Reconcile child Deployment
	deployExists, baselineDeployment, baselineService, deployErr := r.fetchAndReconcileDeployment(ctx, &ephemeral, childName, desiredReplicas)
	if deployErr != nil || baselineDeployment == nil {
		return deployErrResult(deployErr, deployExists)
	}

	// Reconcile child Service
	if err := r.reconcileChildService(ctx, &ephemeral, childName, baselineDeployment, baselineService); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile child HTTPRoute
	httpRoute, err := r.reconcileChildHTTPRoute(ctx, &ephemeral, childName, headerName, headerMatch, gatewayRef, servicePort)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Evaluate Composite Readiness Gate
	var ephemeralDeployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: childName, Namespace: ephemeral.Namespace}, &ephemeralDeployment); err != nil {
		return ctrl.Result{}, err
	}

	return r.evaluateReadinessGate(ctx, &ephemeral, &ephemeralDeployment, httpRoute, childName, desiredReplicas)
}

func deployErrResult(err error, deployExists bool) (ctrl.Result, error) {
	if err != nil {
		return ctrl.Result{}, err
	}
	if !deployExists {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *EphemeralDeploymentReconciler) handleTTL(ctx context.Context, ephemeral *sandglassv1alpha1.EphemeralDeployment) (ctrl.Result, bool, error) {
	log := logf.FromContext(ctx)

	if ephemeral.Spec.TTL == nil {
		return ctrl.Result{}, false, nil
	}

	if ephemeral.Status.ExpiresAt == nil {
		expiresAt := metav1.NewTime(ephemeral.CreationTimestamp.Add(ephemeral.Spec.TTL.Duration))
		ephemeral.Status.ExpiresAt = &expiresAt
		TTLDurationSeconds.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment).Observe(ephemeral.Spec.TTL.Seconds())
		EphemeralDeploymentsCreatedTotal.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment).Inc()
		if err := r.Status().Update(ctx, ephemeral); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, true, nil
			}
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{Requeue: true}, true, nil
	}

	if time.Now().After(ephemeral.Status.ExpiresAt.Time) {
		log.Info("EphemeralDeployment expired, deleting", "expiresAt", ephemeral.Status.ExpiresAt.Time)
		r.Recorder.Eventf(ephemeral, corev1.EventTypeNormal, "TTLExpired",
			"EphemeralDeployment expired at %s; deleting", ephemeral.Status.ExpiresAt.Format(time.RFC3339))
		EphemeralDeploymentsPurgedTotal.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment, "ttl_expired").Inc()
		EphemeralDeploymentsActive.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment).Set(0)
		if err := r.Delete(ctx, ephemeral); err != nil {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{}, true, nil
	}

	return ctrl.Result{}, false, nil
}

func (r *EphemeralDeploymentReconciler) fetchAndReconcileDeployment(
	ctx context.Context,
	ephemeral *sandglassv1alpha1.EphemeralDeployment,
	childName string,
	desiredReplicas int32,
) (bool, *appsv1.Deployment, *corev1.Service, error) {
	log := logf.FromContext(ctx)

	var existingDeployment appsv1.Deployment
	deployExists := true
	if err := r.Get(ctx, types.NamespacedName{Name: childName, Namespace: ephemeral.Namespace}, &existingDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			deployExists = false
		} else {
			log.Error(err, "Unable to fetch existing ephemeral Deployment")
			return false, nil, nil, err
		}
	}

	var baselineDeployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: ephemeral.Spec.TargetDeployment, Namespace: ephemeral.Namespace}, &baselineDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			if !deployExists {
				log.Info("Baseline Deployment not found, waiting", "deployment", ephemeral.Spec.TargetDeployment)
				if condErr := r.setConditionAndPhase(ctx, ephemeral,
					metav1.ConditionFalse, "BaselineNotFound",
					fmt.Sprintf("Baseline Deployment %q not found; retrying every 10s", ephemeral.Spec.TargetDeployment),
					"Provisioning",
				); condErr != nil {
					return deployExists, nil, nil, condErr
				}
				return deployExists, nil, nil, nil
			}
		} else {
			log.Error(err, "Unable to fetch baseline Deployment")
			return deployExists, nil, nil, err
		}
	}

	baselineService := r.findBaselineService(ctx, ephemeral.Namespace, &baselineDeployment)

	ephemeralDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName,
			Namespace: ephemeral.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ephemeralDeployment, func() error {
		if !deployExists {
			ephemeralDeployment.Spec = *baselineDeployment.Spec.DeepCopy()
			ephemeralDeployment.Spec.Replicas = &desiredReplicas
			ephemeralDeployment.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelAppEnv:  envEphemeral,
					labelAppName: childName,
				},
			}
		} else if ephemeral.Spec.Replicas != nil {
			ephemeralDeployment.Spec.Replicas = ephemeral.Spec.Replicas
		}

		ephemeralDeployment.Spec.Template.Labels = map[string]string{
			labelAppEnv:  envEphemeral,
			labelAppName: childName,
		}

		if ephemeral.Spec.PodPatch != nil {
			if err := applyPodPatch(&ephemeralDeployment.Spec.Template, ephemeral.Spec.PodPatch); err != nil {
				return err
			}
			if ephemeralDeployment.Spec.Template.Labels == nil {
				ephemeralDeployment.Spec.Template.Labels = make(map[string]string)
			}
			ephemeralDeployment.Spec.Template.Labels[labelAppEnv] = envEphemeral
			ephemeralDeployment.Spec.Template.Labels[labelAppName] = childName
		}

		return ctrl.SetControllerReference(ephemeral, ephemeralDeployment, r.Scheme)
	})
	if err != nil {
		log.Error(err, "Unable to reconcile Ephemeral Deployment")
		r.Recorder.Eventf(ephemeral, corev1.EventTypeWarning, "ReconcileError",
			"Failed to reconcile Deployment %q: %v", childName, err)
		if condErr := r.setConditionAndPhase(ctx, ephemeral,
			metav1.ConditionFalse, "DeploymentReconcileError", err.Error(), "Failed",
		); condErr != nil {
			return deployExists, nil, nil, condErr
		}
		return deployExists, nil, nil, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Ephemeral Deployment", "operation", op, "name", childName)
		r.Recorder.Eventf(ephemeral, corev1.EventTypeNormal, "DeploymentReconciled",
			"Deployment %q %s", childName, op)
	}

	return deployExists, &baselineDeployment, baselineService, nil
}

func (r *EphemeralDeploymentReconciler) findBaselineService(ctx context.Context, namespace string, baselineDeployment *appsv1.Deployment) *corev1.Service {
	var serviceList corev1.ServiceList
	if err := r.List(ctx, &serviceList, client.InNamespace(namespace)); err != nil || baselineDeployment.Spec.Selector == nil {
		return nil
	}
	for i, svc := range serviceList.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		isMatch := true
		for k, v := range svc.Spec.Selector {
			if baselineDeployment.Spec.Selector.MatchLabels[k] != v {
				isMatch = false
				break
			}
		}
		if isMatch {
			return &serviceList.Items[i]
		}
	}
	return nil
}

func applyPodPatch(target *corev1.PodTemplateSpec, patch *corev1.PodTemplateSpec) error {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	originalJSON, err := json.Marshal(target)
	if err != nil {
		return err
	}
	patchedJSON, err := strategicpatch.StrategicMergePatch(originalJSON, patchJSON, corev1.PodTemplateSpec{})
	if err != nil {
		return err
	}
	return json.Unmarshal(patchedJSON, target)
}

func (r *EphemeralDeploymentReconciler) reconcileChildService(
	ctx context.Context,
	ephemeral *sandglassv1alpha1.EphemeralDeployment,
	childName string,
	baselineDeployment *appsv1.Deployment,
	baselineService *corev1.Service,
) error {
	log := logf.FromContext(ctx)

	ephemeralService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName,
			Namespace: ephemeral.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ephemeralService, func() error {
		ephemeralService.Spec.Ports = buildServicePorts(baselineDeployment, baselineService)
		ephemeralService.Spec.Selector = map[string]string{
			labelAppEnv:  envEphemeral,
			labelAppName: childName,
		}
		ephemeralService.Spec.Type = corev1.ServiceTypeClusterIP

		return ctrl.SetControllerReference(ephemeral, ephemeralService, r.Scheme)
	})
	if err != nil {
		log.Error(err, "Unable to reconcile Ephemeral Service")
		r.Recorder.Eventf(ephemeral, corev1.EventTypeWarning, "ReconcileError",
			"Failed to reconcile Service %q: %v", childName, err)
		if condErr := r.setConditionAndPhase(ctx, ephemeral,
			metav1.ConditionFalse, "ServiceReconcileError", err.Error(), "Failed",
		); condErr != nil {
			return condErr
		}
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Ephemeral Service", "operation", op, "name", childName)
		r.Recorder.Eventf(ephemeral, corev1.EventTypeNormal, "ServiceReconciled",
			"Service %q %s", childName, op)
	}
	return nil
}

func buildServicePorts(baselineDeployment *appsv1.Deployment, baselineService *corev1.Service) []corev1.ServicePort {
	if baselineService != nil {
		ports := make([]corev1.ServicePort, 0, len(baselineService.Spec.Ports))
		for _, port := range baselineService.Spec.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       port.Name,
				Port:       port.Port,
				TargetPort: port.TargetPort,
				Protocol:   port.Protocol,
			})
		}
		return ports
	}

	var ports []corev1.ServicePort
	for _, container := range baselineDeployment.Spec.Template.Spec.Containers {
		for _, port := range container.Ports {
			proto := port.Protocol
			if proto == "" {
				proto = corev1.ProtocolTCP
			}
			ports = append(ports, corev1.ServicePort{
				Name:       port.Name,
				Port:       port.ContainerPort,
				TargetPort: intstr.FromInt(int(port.ContainerPort)),
				Protocol:   proto,
			})
		}
	}
	if len(ports) == 0 {
		ports = []corev1.ServicePort{{
			Name:       portNameHTTP,
			Port:       defaultPort80,
			TargetPort: intstr.FromInt(defaultPort80),
			Protocol:   corev1.ProtocolTCP,
		}}
	}
	return ports
}

func (r *EphemeralDeploymentReconciler) reconcileChildHTTPRoute(
	ctx context.Context,
	ephemeral *sandglassv1alpha1.EphemeralDeployment,
	childName, headerName, headerMatch, gatewayRef string,
	servicePort *intstr.IntOrString,
) (*gatewayv1.HTTPRoute, error) {
	log := logf.FromContext(ctx)

	var persistedService corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Name: childName, Namespace: ephemeral.Namespace}, &persistedService); err != nil {
		log.Error(err, "Unable to fetch persisted Ephemeral Service for HTTPRoute port resolution")
		return nil, err
	}

	targetPort := resolveTargetPort(&persistedService, servicePort)

	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName,
			Namespace: ephemeral.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, httpRoute, func() error {
		exactType := gatewayv1.HeaderMatchExact
		weight := int32(100)
		gatewayName := gatewayv1.ObjectName(gatewayRef)

		httpRoute.Spec.ParentRefs = []gatewayv1.ParentReference{
			{
				Name: gatewayName,
			},
		}

		httpRoute.Spec.Rules = []gatewayv1.HTTPRouteRule{
			{
				Matches: []gatewayv1.HTTPRouteMatch{
					{
						Headers: []gatewayv1.HTTPHeaderMatch{
							{
								Type:  &exactType,
								Name:  gatewayv1.HTTPHeaderName(headerName),
								Value: headerMatch,
							},
						},
					},
				},
				BackendRefs: []gatewayv1.HTTPBackendRef{
					{
						BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: gatewayv1.ObjectName(childName),
								Port: &targetPort,
							},
							Weight: &weight,
						},
					},
				},
			},
		}

		return ctrl.SetControllerReference(ephemeral, httpRoute, r.Scheme)
	})
	if err != nil {
		log.Error(err, "Unable to reconcile HTTPRoute")
		r.Recorder.Eventf(ephemeral, corev1.EventTypeWarning, "ReconcileError",
			"Failed to reconcile HTTPRoute %q: %v", childName, err)
		if condErr := r.setConditionAndPhase(ctx, ephemeral,
			metav1.ConditionFalse, "HTTPRouteReconcileError", err.Error(), "Failed",
		); condErr != nil {
			return nil, condErr
		}
		return nil, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled HTTPRoute", "operation", op, "name", childName)
		r.Recorder.Eventf(ephemeral, corev1.EventTypeNormal, "HTTPRouteReconciled",
			"HTTPRoute %q %s", childName, op)
	}

	return httpRoute, nil
}

func (r *EphemeralDeploymentReconciler) evaluateReadinessGate(
	ctx context.Context,
	ephemeral *sandglassv1alpha1.EphemeralDeployment,
	ephemeralDeployment *appsv1.Deployment,
	httpRoute *gatewayv1.HTTPRoute,
	childName string,
	desiredReplicas int32,
) (ctrl.Result, error) {
	deployReady := ephemeralDeployment.Status.ReadyReplicas >= desiredReplicas || desiredReplicas == 0

	routeProgrammed := true
	if len(httpRoute.Status.Parents) > 0 {
		routeProgrammed = false
		for _, parent := range httpRoute.Status.Parents {
			for _, cond := range parent.Conditions {
				if (cond.Type == string(gatewayv1.RouteConditionAccepted) || cond.Type == "Programmed") &&
					cond.Status == metav1.ConditionTrue {
					routeProgrammed = true
					break
				}
			}
		}
	}

	if deployReady && routeProgrammed {
		msg := fmt.Sprintf("Ephemeral Deployment %q is ready (%d/%d replica(s))",
			childName, ephemeralDeployment.Status.ReadyReplicas, desiredReplicas)
		EphemeralDeploymentsActive.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment).Set(1)
		if condErr := r.setConditionAndPhase(ctx, ephemeral,
			metav1.ConditionTrue, "DeploymentReady", msg, "Ready",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
	} else {
		EphemeralDeploymentsActive.WithLabelValues(ephemeral.Namespace, ephemeral.Spec.TargetDeployment).Set(0)
		var msg string
		if !deployReady {
			msg = fmt.Sprintf("Ephemeral Deployment %q has %d/%d ready replicas",
				childName, ephemeralDeployment.Status.ReadyReplicas, desiredReplicas)
		} else {
			msg = fmt.Sprintf("HTTPRoute %q waiting for Gateway acceptance", childName)
		}
		if condErr := r.setConditionAndPhase(ctx, ephemeral,
			metav1.ConditionFalse, "ProvisioningNotReady", msg, "Provisioning",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if ephemeral.Spec.TTL != nil && ephemeral.Status.ExpiresAt != nil {
		requeueAfter := time.Until(ephemeral.Status.ExpiresAt.Time)
		if requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
	}

	return ctrl.Result{}, nil
}

func getRoutingDefaults(ephemeral *sandglassv1alpha1.EphemeralDeployment) (headerName, headerMatch, gatewayRef string, servicePort *intstr.IntOrString) {
	headerName = "X-Sandglass"
	headerMatch = ephemeral.Name
	gatewayRef = "main-gateway"

	if ephemeral.Spec.Routing != nil {
		if ephemeral.Spec.Routing.HeaderName != "" {
			headerName = ephemeral.Spec.Routing.HeaderName
		}
		if ephemeral.Spec.Routing.HeaderMatch != "" {
			headerMatch = ephemeral.Spec.Routing.HeaderMatch
		}
		if ephemeral.Spec.Routing.GatewayRef != "" {
			gatewayRef = ephemeral.Spec.Routing.GatewayRef
		}
		servicePort = ephemeral.Spec.Routing.ServicePort
	}
	return headerName, headerMatch, gatewayRef, servicePort
}

func getDesiredReplicas(ephemeral *sandglassv1alpha1.EphemeralDeployment) int32 {
	if ephemeral.Spec.Replicas != nil {
		return *ephemeral.Spec.Replicas
	}
	return 1
}

func resolveTargetPort(persistedService *corev1.Service, servicePort *intstr.IntOrString) gatewayv1.PortNumber {
	if servicePort != nil {
		if servicePort.Type == intstr.Int {
			return gatewayv1.PortNumber(servicePort.IntVal)
		}
		for _, p := range persistedService.Spec.Ports {
			if p.Name == servicePort.StrVal {
				return gatewayv1.PortNumber(p.Port)
			}
		}
	}
	for _, p := range persistedService.Spec.Ports {
		if p.Name == portNameHTTP || p.Name == portNameWeb || p.Name == portNameHTTPS {
			return gatewayv1.PortNumber(p.Port)
		}
	}
	for _, p := range persistedService.Spec.Ports {
		if p.Port == defaultPort80 {
			return gatewayv1.PortNumber(defaultPort80)
		}
	}
	if len(persistedService.Spec.Ports) > 0 {
		return gatewayv1.PortNumber(persistedService.Spec.Ports[0].Port)
	}
	return gatewayv1.PortNumber(defaultPort80)
}

// setConditionAndPhase updates status.conditions, status.phase, and status.active.
func (r *EphemeralDeploymentReconciler) setConditionAndPhase(
	ctx context.Context,
	ephemeral *sandglassv1alpha1.EphemeralDeployment,
	status metav1.ConditionStatus,
	reason, message, phase string,
) error {
	log := logf.FromContext(ctx)

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: ephemeral.Generation,
	}
	meta.SetStatusCondition(&ephemeral.Status.Conditions, condition)
	ephemeral.Status.Phase = phase
	ephemeral.Status.Active = status == metav1.ConditionTrue

	if err := r.Status().Update(ctx, ephemeral); err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Status update conflict, another reconcile loop will handle it", "phase", phase)
			return nil
		}
		log.Error(err, "Unable to update EphemeralDeployment status", "phase", phase)
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sandglassv1alpha1.EphemeralDeployment{},
		".spec.targetDeployment",
		func(obj client.Object) []string {
			ed := obj.(*sandglassv1alpha1.EphemeralDeployment)
			if ed.Spec.TargetDeployment == "" {
				return nil
			}
			return []string{ed.Spec.TargetDeployment}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sandglassv1alpha1.EphemeralDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
