# Gateway API Status Propagation and End-to-End Readiness Gate

To prevent premature "Ready" signals in CI/CD pipelines before preview traffic is routable, Sandglass evaluates both pod replica readiness and underlying `HTTPRoute` parent condition status (`Accepted=True` and `Programmed=True`) before marking an `EphemeralDeployment` as active and ready.
