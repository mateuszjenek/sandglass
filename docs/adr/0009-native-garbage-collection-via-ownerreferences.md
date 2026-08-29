# Native Garbage Collection via OwnerReferences

Sandglass relies exclusively on native Kubernetes `OwnerReferences` (via `SetControllerReference`) for managing child lifecycle and teardown. It intentionally avoids custom finalizers to eliminate finalizer deadlocks during namespace deletions, ensuring clean and automated cascading garbage collection when an `EphemeralDeployment` is deleted or expires via TTL.
