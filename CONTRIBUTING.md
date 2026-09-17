# Contributing to tailscale-gateway

A Kubernetes controller that integrates the Gateway API with Tailscale Serve to expose cluster services onto your Tailnet

## Workflow

Fork, branch off `main`, open a PR against `main`. One logical change per PR.

## Environment

```sh
direnv allow  # or: nix develop
```

## Validation

`go test ./...` green before submitting.

Security issues: see [SECURITY.md](SECURITY.md).
