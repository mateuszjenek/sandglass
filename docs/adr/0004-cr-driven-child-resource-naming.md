# Child Resource Naming Driven by Custom Resource Name

To guarantee strict compliance with the Kubernetes 63-character RFC 1123 resource naming limit and prevent naming collisions, Sandglass names all managed child resources (Deployment, Service, HTTPRoute) identically to the parent `EphemeralDeployment.metadata.name`, rather than concatenating the target deployment and header match strings.
