# Header-Based Ingress and East-West Routing via Gateway API

To route traffic to preview environments without full namespace duplicates, Sandglass standardizes on Kubernetes Gateway API `HTTPRoute` resources matching the `X-Sandglass` header for ingress, relying on standard distributed trace / header propagation for internal east-west service calls. This keeps the operator lightweight, avoiding the complexity of building a custom service mesh or rewriting cluster-internal DNS endpoints.
