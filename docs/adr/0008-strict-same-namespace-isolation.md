# Strict Same-Namespace Workload Isolation

All managed ephemeral resources (Deployment, Service, HTTPRoute) and the targeted baseline Deployment must strictly reside within the same Kubernetes namespace. Cross-namespace target referencing is explicitly disallowed to enforce least privilege security, simplify RBAC, and eliminate cross-namespace secret and config leakage.
