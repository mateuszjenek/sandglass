# Stateless Workload Isolation with Strategic Merge Pod Patching

For `v1alpha1`, `EphemeralDeployment` is strictly scoped to stateless `apps/v1.Deployment` workloads and creates an isolated `core/v1.Service`, applying environment, container, and image modifications exclusively through `spec.podPatch` via Strategic Merge Patch. Stateful resources (databases, shared volumes) and automated ConfigMap/Secret cloning are intentionally deferred to future versions to maintain a lean, robust controller core.
