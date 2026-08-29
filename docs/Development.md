<!-- owner: shikanime | zone: internal | purpose: local dev setup and build/test loop -->

# Development

## Prerequisites

- Go toolchain (see `go.mod`).
- `nix` + `direnv` for the reproducible dev shell (`direnv allow`, or
  `nix develop`). Provides `go`, `kubectl`, `skaffold`, and `nix fmt`.
- A Kubernetes cluster `kubectl` can reach, with Gateway API CRDs
  (`gateway.networking.k8s.io/v1`) installed.

## Build

```sh
go build ./...
go build -o /tmp/tsgw ./cmd/tailscale-gateway-controller
```

## Format and lint

```sh
nix fmt            # format all sources (Markdown 80 cols, Go, Nix)
go vet ./...
```

Keep Markdown wrapped at 80 columns and run `nix fmt` before committing.

## Test loop

Unit tests:

```sh
go test ./...
```

The e2e suite talks to a live cluster and is gated behind the `e2e` build tag so
the normal `go test ./...` stays green in CI:

```sh
kubectl create namespace tailscale-system --dry-run=client -o yaml | \
  kubectl apply -f -
skaffold run
TMPDIR=/private/tmp GOCACHE=/private/tmp/tailscale-gateway-gocache \
  GOFLAGS=-mod=mod go test -tags e2e ./e2e -count=1
```

The e2e run generates unique resource names per run, so it is repeatable against
the same cluster.

## Make a change

- Use a worktree or branch off `main@origin`; one logical change per PR.
- Signed-off-by is required (DCO) — the global `jj` config adds it
  automatically; do not hand-edit it.
- Protect `main`: 1 approving review, linear history, signed commits,
  squash+rebase merge only.
