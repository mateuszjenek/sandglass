# Contributing to Sandglass

Thank you for your interest in contributing to **Sandglass**! We welcome bug reports, feature requests, documentation improvements, and code contributions.

---

## 🛠️ Development Setup

Sandglass uses [`mise`](https://mise.jdx.dev/) for deterministic toolchain and task management.

### 1. Prerequisites

- [Docker](https://docs.docker.com/get-docker/) or compatible container runtime
- [`mise`](https://mise.jdx.dev/getting-started.html)

Install tool dependencies managed by `mise` (Go, `kustomize`, `kind`, `golangci-lint`):

```bash
mise install
```

---

## 🧪 Testing Workflow

All code contributions must include tests and pass existing suites.

### Unit & Integration Tests (Envtest)

Runs the Ginkgo/Gomega suite against a real Kubernetes API Server and etcd:

```bash
mise run test
```

### End-to-End Tests (Kind)

Runs the comprehensive e2e test suite against an isolated [Kind](https://kind.sigs.k8s.io/) cluster with Envoy Gateway:

```bash
mise run test-e2e
```

### Linting & Formatting

Check and auto-fix formatting and lint issues:

```bash
mise run lint
mise run lint-fix
```

### Code Generation & Manifests

Whenever you modify CRD types (`api/v1alpha1/*_types.go`) or RBAC markers:

```bash
mise run manifests
mise run generate
```

---

## 📝 Pull Request Guidelines

1. **Follow Conventional Commits**: Structure your commits cleanly (`feat:`, `fix:`, `docs:`, `test:`, `chore:`).
2. **Keep PRs Focused**: One logical change or feature per pull request.
3. **Update Documentation**: When changing user-facing APIs or controller behavior, update `README.md`, `CONTEXT.md`, and relevant Architecture Decision Records (`docs/adr/`).
4. **Pass CI**: Ensure unit tests, e2e tests, and linters pass before submitting.
