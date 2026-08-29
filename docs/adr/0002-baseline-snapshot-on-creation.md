# Baseline Snapshotting on Creation

When an `EphemeralDeployment` is created, Sandglass takes a one-time snapshot of the baseline Deployment spec and applies the `PodPatch`, rather than continuously synchronizing subsequent baseline changes into the running ephemeral workload. This guarantees deterministic behavior during pull request review and automated testing, preventing external baseline rollouts from disrupting active test sessions.
