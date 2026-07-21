# pgfleet documentation

- **[architecture.md](architecture.md)**: the packages, how a `migrate up` run
  flows through them, the drift subsystem, and where the dashboard fits.
- **[design-decisions.md](design-decisions.md)**: why the dashboard is built
  the way it is: same repo, reusing `internal/`, read-only, the `Provider`
  interface, the TTL cache, the committed build artifact. Each entry states the
  alternative and the cost.
- **[engineering-log.md](engineering-log.md)**: bugs and surprises from the
  build, with how each was found: the blank-page deep-link bug, the error-state
  rewrite, why a tenant behind on migrations also reports as drifted, and how
  the Docker build was validated without Docker.
- **[design-faq.md](design-faq.md)**: common questions about how it works and
  why, answered from the code, including the known limitations and what is
  deliberately not measured.
- **[deploy-aws.md](deploy-aws.md)**: deploying the dashboard on EC2 with
  Docker: console walkthrough, security groups, troubleshooting, teardown.

The full requirements are in [the specification](../pgfleet-spec.md).
