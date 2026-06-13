# Tailscale Gateway

A Kubernetes controller that provisions a Tailscale-based Gateway, integrating the Gateway API with Tailscale Serve to expose cluster services onto your Tailnet.

**Language:** Go

## Structure

- `main.go` — Controller entry point
- `controllers/` — Reconciler logic
- `apis/` — Custom resource definitions
- `config/` — Kubernetes manifests and RBAC
- `flake.nix` — Nix development shell and CI configuration

## Commit Style

- Plain-text capitalized title, no conventional-commit prefix
- Body with labels: `Design:`, `Related:`, `Closes #`
- Keep Markdown lines wrapped at 80 columns and run `nix fmt` before shipping

## Stack

- 1 commit == 1 PR via ghstack
- Amend + `ghstack` to resubmit
- `ghstack land` on head PR to land the entire stack
- Never `gh pr merge` (creates poisoned commits)
- Never force-push ghstack branches
- ghstack only works on HEAD commit chains, not detached HEADs

## Protect `main`

- Require 1 approving review
- Require linear history (no merge commits)
- Require signed commits
- Squash+rebase merge only

*Licensed under Apache-2.0. Signed-off-by required on all commits*