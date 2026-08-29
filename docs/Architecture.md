<!-- owner: shikanime | zone: internal | purpose: module layout and reconcile design intent -->

# Architecture

Tailscale Gateway is a Go controller
(`github.com/shikanime-studio/tailscale-gateway`) that binds Gateway API
resources to Tailscale devices on your Tailnet.

## Top-level layout

- `cmd/tailscale-gateway-controller/` — binary entry point; wires config, the
  controller manager, and the Tailscale client.
- `internal/config/` — reads env into a typed config (OAuth, tags, image).
- `internal/controller/` — reconciler(s) for `Gateway` and `Service` resources;
  the core watch/reconcile logic.
- `internal/tailscale/` — Tailscale client wrapper; auth-key generation via
  `tailscale.com/client/tailscale`.
- `internal/apiutil/`, `internal/applyconfig/` — Gateway API helpers and
  apply-config builders.
- `internal/reconcilerutil/` — shared reconcile helpers.
- `manifests/gateway/` — Kustomize overlay: controller Deployment, RBAC,
  ServiceAccount, metrics Service, and the `tailscale` `GatewayClass`.
- `manifests/demo/` — sample `Gateway` + `HTTPRoute` for a smoke test.
- `e2e/` — live-cluster e2e suite (behind the `e2e` build tag).
- `skaffold.yaml` — dev deploy of the controller into a local cluster.

## Design intent

- **Gateway API as the source of truth.** The controller watches `Gateway`
  (bound to `GatewayClass` `tailscale`) and `HTTPRoute` and reflects them onto
  Tailscale Serve.
- **Service exposure shortcut.** A `Service` of `type: LoadBalancer` with
  `loadBalancerClass: tailscale` plus the hostname annotation is exposed without
  a `Gateway` object.
- **Ephemeral auth keys.** For each `Gateway`, the controller looks for a Secret
  named `<gateway-name>` in the Gateway namespace. If it carries `authkey`, that
  is reused; otherwise the controller mints a non-reusable, preauthorized key
  from the OAuth client, tagged from `TAILSCALE_TAGS`.
- **No state store.** Reconciliation is stateless; Tailscale is the external
  source of truth for device/serve state.

## Boundaries

- The controller creates only cluster-scoped Gateway API + Tailscale objects; it
  never mutates app workloads directly.
- Tailscale OAuth client must have scopes permitting key creation.
