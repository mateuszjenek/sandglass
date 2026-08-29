# Default Single Replica Scaling for Ephemeral Clones

To prevent ephemeral pull request environments from depleting cluster compute capacity, Sandglass automatically defaults the replica count of cloned Ephemeral Deployments to 1, regardless of how many replicas the baseline Deployment runs. Higher replica counts must be explicitly declared via `podPatch` when load testing a preview is intended.
