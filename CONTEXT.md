# Sandglass

Sandglass is a Kubernetes operator that provisions instant, cost-effective ephemeral preview environments by cloning baseline workloads and dynamically routing traffic using Gateway API headers.

## Language

**Baseline Deployment**:
The stable, long-running Deployment in a shared namespace that serves default traffic and acts as the template for ephemeral clones.
_Avoid_: Primary deployment, main workload, upstream deployment

**Ephemeral Deployment**:
An isolated, short-lived clone of a Baseline Deployment created for testing pull requests or feature branches.
_Avoid_: PR environment, dynamic preview, duplicate deployment

**Pod Patch**:
A partial PodTemplateSpec merged into the Baseline Deployment template using Strategic Merge Patch to inject preview images and environment variables.
_Avoid_: Container override, pod mutation

**Routing Spec**:
The dedicated configuration block defining ingress routing parameters, including header names, match values, target service ports, and gateway references.
_Avoid_: Ingress config, network settings

**Routing Header**:
The HTTP request header (defaulting to `X-Sandglass`) that instructs Gateway API HTTPRoutes to steer traffic to the Ephemeral Deployment.
_Avoid_: Environment tag, preview cookie, routing key

**Header Match**:
The unique identifier value matched against the Routing Header to select a specific Ephemeral Deployment, defaulting to the custom resource name.
_Avoid_: Environment name, branch tag, PR identifier

**Baseline Snapshot**:
The frozen copy of the Baseline Deployment spec captured at creation time to ensure predictable preview execution without unexpected drift.
_Avoid_: Baseline sync, live template

**Disposable Lifecycle**:
The operational philosophy that ephemeral environments are transient snapshots designed to be deleted and recreated rather than continually mutated.
_Avoid_: In-place update, persistent preview

**TTL (Time to Live)**:
The finite duration after which an Ephemeral Deployment and its associated resources are automatically purged by the operator, renewable via declarative spec updates or touch annotations.
_Avoid_: Expiration timer, retention period

**Readiness Gate**:
The composite status condition verifying both running workload pods and confirmed Gateway API route programming before declaring a preview environment active.
_Avoid_: Pod readiness, route check

**Replica Defaulting**:
The automatic scaling down of ephemeral clones to 1 replica to protect cluster resources, regardless of the baseline's replica count.
_Avoid_: Replicas copy, baseline scale
