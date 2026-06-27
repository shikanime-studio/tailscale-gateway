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

## Stack

- 1 commit == 1 PR via ghstack (1 commit is 1 logical atomic change)
- The commit title **is** the PR title; the commit body **is** the PR body
- Split work into stacked PRs to keep each PR small and reviewable
- To pull down an existing stack: `ghstack checkout <PR_NUMBER>`
- To update a PR: edit files, then `jj squash` (or `git commit --amend`) into
  the **target commit** of the stack — the one that PR represents; the commit
  message updates the PR title and body automatically when resubmitted
- Resubmit with `ghstack` after squashing
- `ghstack land` on the head PR to land the entire stack
- Never `gh pr merge` (creates poisoned commits)
- Never force-push ghstack branches

## Protect `main`

- Require 1 approving review
- Require linear history (no merge commits)
- Require signed commits
- Squash+rebase merge only

_Licensed under Apache-2.0. Signed-off-by required on all commits. Always use
worktrees when making changes._
