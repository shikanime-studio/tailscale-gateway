<!-- owner: shikanime | zone: internal | purpose: env, annotation, and config surface -->

# Reference

## Environment variables (controller Secret)

Read by `internal/config`:

- `TAILSCALE_OAUTH_CLIENT_ID` — OAuth client ID (key-creation scope).
- `TAILSCALE_OAUTH_CLIENT_SECRET` — OAuth client secret.
- `TAILSCALE_TAGS` — comma-separated device tags for generated keys (e.g.
  `tag:gateway,tag:proxy`); defaults to `tag:gateway`.
- `TAILSCALE_IMAGE` — Tailscale daemon image; default
  `tailscale/tailscale:latest`.

## Service annotation

- `tailscale.gateway.shikanime.studio/hostname` — the Tailnet hostname a
  `LoadBalancer` Service with `loadBalancerClass: tailscale` is exposed as.

## GatewayClass

- Name: `tailscale` (created by `manifests/gateway`).

## CLI / binary

- `cmd/tailscale-gateway-controller` — the controller binary; flags are minimal
  (addr/metrics wiring). Behavior is driven by the Secret env and the Gateway
  API resources it watches.

## Key concepts

- **Gateway** — Gateway API object bound to `GatewayClass tailscale`.
- **HTTPRoute** — routes traffic into backing Services.
- **Auth key** — per-Gateway Tailscale device credential; ephemeral unless a
  `<gateway-name>` Secret `authkey` is provided.
- **Build tag `e2e`** — gates the live-cluster test suite in `e2e/`.
