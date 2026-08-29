# CR-Name Derived Header Matching and Collision Prevention

By defaulting the `routing.headerMatch` value directly to the `EphemeralDeployment.metadata.name`, Sandglass leverages Kubernetes's native per-namespace name uniqueness to prevent route collision by construction. Custom header matches remain supported for specific alias requirements, with collision detection enforced via validating webhooks and status conditions.
