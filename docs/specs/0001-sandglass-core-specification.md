# Spec: Sandglass Ephemeral Preview Environments Operator

**Triage Label**: `ready-for-agent`

---

## Problem Statement

Engineering teams developing microservices and web applications on Kubernetes need fast, realistic preview environments for pull requests without incurring the high cloud costs, slow setup times, and maintenance burden of duplicating entire namespaces or running complete replica clusters. Furthermore, spinning up full service replicas per PR often leads to resource exhaustion, route conflicts, and stale preview workloads lingering indefinitely in the cluster.

---

## Solution

Sandglass provides a lightweight, Kubernetes-native operator that dynamically clones a single **Baseline Deployment** into an isolated **Ephemeral Deployment** for a given pull request. By applying an inlined **Pod Patch** via Kubernetes Strategic Merge Patch and orchestrating standard Gateway API `HTTPRoute` resources with the **Routing Header** (`X-Sandglass`), Sandglass steers traffic with specific **Header Match** values directly to the preview workload while unmodified traffic flows seamlessly to the baseline. Ephemeral clones are governed by an automated **TTL** and **Disposable Lifecycle**, ensuring zero orphaned compute resources.

---

## User Stories

1. As a software engineer opening a pull request, I want to deploy an Ephemeral Deployment targeting my baseline service with a single YAML manifest, so that I can immediately preview my changes in a real cluster environment.
2. As a software engineer, I want my Ephemeral Deployment's routing rules to default to my Custom Resource name, so that I don't have to manually configure unique header match values to avoid routing collisions with teammates.
3. As a software engineer, I want to supply a custom Header Match alias in `spec.routing.headerMatch`, so that I can route traffic using clean human-readable tags like `pr-1234`.
4. As a frontend developer, I want to route test requests through our main Gateway by sending the `X-Sandglass` HTTP header, so that I can inspect my preview backend without altering production ingress or DNS records.
5. As an infrastructure engineer, I want to customize the Routing Header key (e.g., using `X-Preview-ID` instead of `X-Sandglass`), so that Sandglass integrates seamlessly with our organization's existing ingress conventions.
6. As a platform engineer, I want Ephemeral Deployments to take a Baseline Snapshot upon creation rather than continuously syncing live baseline updates, so that running automated end-to-end tests on a PR are completely deterministic.
7. As a platform engineer, I want Ephemeral Deployments to default to 1 replica regardless of whether the baseline has 10 replicas, so that active PR previews do not consume unnecessary CPU and memory.
8. As a performance engineer, I want to optionally specify higher replica counts in `spec.replicas` or `spec.podPatch`, so that I can load test a feature branch when needed.
9. As a developer, I want all child resources (Deployment, Service, HTTPRoute) to share the exact name of the parent Ephemeral Deployment, so that resource names never exceed Kubernetes's 63-character RFC 1123 limit.
10. As a DevOps engineer, I want Ephemeral Deployments to automatically delete themselves after a configurable TTL duration, so that abandoned feature branches never leave zombie workloads running in our clusters.
11. As a developer actively testing a PR, I want to extend an expiring preview environment by updating `spec.ttl` or applying a touch annotation, so that my preview remains available while I finish my manual testing.
12. As a CI/CD pipeline, I want Ephemeral Deployment status to report `Ready` only when both workload pods are running and the Gateway API controller has confirmed `HTTPRoute` programming, so that test suites don't fail due to premature traffic cutover.
13. As a cluster operator, I want all child resources and baseline workloads to remain strictly contained within the same Kubernetes namespace, so that secrets and configurations are never leaked across security boundaries.
14. As a site reliability engineer, I want Sandglass to export Prometheus metrics tracking active previews and TTL purges, so that our team can monitor operator usage, preview lifespans, and cluster cost savings.
15. As a developer running `kubectl get ephemeraldeployments`, I want clear summary columns showing target deployment, routing header key, header match value, lifecycle phase, active state, and remaining TTL, so that I can quickly assess the status of all active preview environments.

---

## Implementation Decisions

### 1. API Schema & Group Structure
- **Group/Version/Kind**: `sandglass.io/v1alpha1`, `Kind: EphemeralDeployment`.
- **`spec.targetDeployment`** (`string`, Required): Name of the baseline `apps/v1.Deployment` in the same namespace to snapshot.
- **`spec.routing`** (`object`, Optional):
  - `headerName` (`string`, Optional, Default: `"X-Sandglass"`): The HTTP header key used for matching.
  - `headerMatch` (`string`, Optional, Default: derived from `metadata.name`): The header value used for exact matching.
  - `gatewayRef` (`string`, Optional, Default: `"main-gateway"`): The name of the Gateway API `Gateway` resource in the same namespace.
  - `servicePort` (`intstr.IntOrString`, Optional): Explicit target service port number or name for routing.
- **`spec.replicas`** (`*int32`, Optional, Default: `1`): Number of desired pods for the ephemeral preview.
- **`spec.ttl`** (`*metav1.Duration`, Optional): Lifespan before automatic cascading deletion.
- **`spec.podPatch`** (`*corev1.PodTemplateSpec`, Optional): Strategic Merge Patch merged into the baseline pod template.
- **`status`**:
  - `phase` (`string`): `Provisioning`, `Ready`, or `Failed`.
  - `active` (`bool`): `true` when fully routable.
  - `expiresAt` (`*metav1.Time`): Calculated timestamp when TTL expires.
  - `conditions` (`[]metav1.Condition`): Standard Kubernetes conditions (`Ready`, `BaselineNotFound`, `DeploymentNotReady`, `RouteNotProgrammed`, `RouteConflict`).

### 2. Controller Reconciliation Workflow
- **Reconciliation Seam**: The controller operates on `EphemeralDeployment` objects and manages child `Deployment`, `Service`, and `HTTPRoute` resources.
- **Snapshotting**: Upon creation, the baseline `Deployment` is fetched and its `Spec` copied into the child deployment. Subsequent baseline mutations do not trigger automatic rollouts on active ephemeral clones.
- **Isolation Labeling**: Child deployments override selectors and pod labels with `app.kubernetes.io/name: <cr-name>` and `app.kubernetes.io/env: ephemeral`.
- **Port Resolution Heuristics**: The controller mirrors ports from the baseline Service matching the baseline deployment's selector, falling back to container ports, and defaulting to port 80. Route target port defaults intelligently (`http` > `web` > `80` > first port) or respects `spec.routing.servicePort`.
- **Readiness Gate**: The controller inspects `Deployment.status.readyReplicas` and `HTTPRoute.status.parents[].conditions` (checking `Type: Programmed` and `Type: Accepted` with `Status: True`) before setting `status.phase: Ready` and `status.active: true`.
- **TTL Purging**: The controller requeues reconciliation at `status.expiresAt`. Once current time passes `expiresAt`, the controller issues a Kubernetes `Delete` call on the `EphemeralDeployment`.
- **Garbage Collection**: All child objects are registered with `ctrl.SetControllerReference` for automatic Kubernetes cascading cleanup without custom finalizers.

### 3. Observability & Printer Columns
- **Prometheus Metrics**: Register collectors for `sandglass_ephemeral_deployments_active`, `sandglass_ephemeral_deployments_created_total`, `sandglass_ephemeral_deployments_purged_total`, and `sandglass_ttl_duration_seconds`.
- **Print Columns**:
  - `Target` (`.spec.targetDeployment`)
  - `HeaderKey` (`.spec.routing.headerName`)
  - `HeaderValue` (`.spec.routing.headerMatch`)
  - `Phase` (`.status.phase`)
  - `Active` (`.status.active`)
  - `ExpiresAt` (`.status.expiresAt`)

---

## Testing Decisions

### Test Quality Principles
- Tests verify external system behavior through public Kubernetes API boundaries and HTTP Gateway traffic, never internal helper methods.
- Tests assert against independent, known-good expected states.

### Target Seams
1. **Controller Integration Seam (`envtest`)**:
   - Reconcile an `EphemeralDeployment` against real Kubernetes API Server + etcd.
   - Verify creation and spec of child `Deployment`, `Service`, and `HTTPRoute`.
   - Verify Strategic Merge Patch application, single-replica defaulting, and status condition updates.
   - Verify TTL expiration calculation and requeue scheduling.
2. **End-to-End System Seam (`Kind` + Envoy Gateway)**:
   - Deploy baseline service and Gateway.
   - Apply `EphemeralDeployment` and wait for `status.phase: Ready`.
   - Verify standard HTTP requests reach the baseline deployment.
   - Verify HTTP requests with `-H "X-Sandglass: <match>"` route to the patched ephemeral deployment.
   - Verify automatic resource purging when TTL elapses.

---

## Out of Scope

- **Stateful Workloads**: Automated cloning and provisioning of persistent volume claims (PVCs) or database schemas.
- **Cross-Namespace Cloning**: Deploying ephemeral environments into namespaces different from their baseline workload.
- **Continuous Baseline Drift Synchronization**: Live propagation of staging configuration mutations into running ephemeral previews (disposable recreate model is enforced instead).
- **Custom Service Mesh / DNS Interception**: Sandglass relies strictly on Gateway API and standard header propagation rather than embedding a proprietary sidecar or DNS server.

---

## Further Notes

- All changes adhere strictly to the vocabulary recorded in [CONTEXT.md](file:///Users/mjenek/GolandProjects/sandglass/CONTEXT.md).
- Architecture decisions follow [ADR 0001 through 0010](file:///Users/mjenek/GolandProjects/sandglass/docs/adr/).
