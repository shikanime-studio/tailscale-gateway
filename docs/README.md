<!-- owner: shikanime | zone: internal | purpose: index and nav for internal ops docs -->

# Tailscale Gateway — Docs

A Kubernetes controller that provisions a Tailscale-based Gateway, wiring the
Gateway API to Tailscale Serve so cluster services are reachable on your
Tailnet. This `docs/` tree is the internal-ops zone (reviewed via PR).

## Internal ops

- [Architecture](./Architecture.md) — modules, reconcile flow, boundaries
- [Development](./Development.md) — local setup, build/format, test loop
- [Runbook](./Runbook.md) — install, deploy, release, branch protection
- [Troubleshooting](./Troubleshooting.md) — known failure modes + fixes
- [Reference](./Reference.md) — env vars, annotations, CLI surface

## User-facing docs

The project README at [README.md](../README.md) is the user-facing entry point
(install, Gateway/HTTPRoute examples, e2e). No separate docs site exists yet;
link out here if one appears later.
