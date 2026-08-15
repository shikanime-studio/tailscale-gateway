# Tailscale Gateway

A Kubernetes controller that provisions a Tailscale-based Gateway, integrating
the Gateway API with Tailscale Serve to expose cluster services onto your
Tailnet.

**Language:** Go

## Structure

- `main.go` — Controller entry point
- `controllers/` — Reconciler logic
- `apis/` — Custom resource definitions
- `config/` — Kubernetes manifests and RBAC
- `flake.nix` — Nix development shell and CI configuration

## Functionality

- Watches Gateway API resources
- Probes Tailscale Serve for service exposure
- Manages Tailnet-facing ingress for cluster workloads

## Commit Style

- Plain-text capitalized title, no conventional-commit prefix
- Body with labels: `Design:`, `Related:`, `Closes #`
- Keep Markdown lines wrapped at 80 columns and run `nix fmt` before shipping

## Protect `main`

- Require 1 approving review
- Require linear history (no merge commits)
- Require signed commits
- Squash+rebase merge only

_Licensed under Apache-2.0. Signed-off-by required on all commits. Always use
worktrees when making changes._
