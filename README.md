# Tailscale Gateway Controller

A Kubernetes controller that provisions a Tailscale-based Gateway. It reconciles supporting resources (ServiceAccount, RBAC, Secret, ConfigMap, DaemonSet) and serves HTTPRoutes via Tailscale Serve.

## Environment Configuration

- `TAILSCALE_TAILNET`: Tailnet identifier used for API calls
- `TAILSCALE_OAUTH_CLIENT_ID`: OAuth client ID with scopes allowing key creation
- `TAILSCALE_OAUTH_CLIENT_SECRET`: OAuth client secret
- `TAILSCALE_TAGS`: Comma-separated device tags applied to generated auth keys (e.g. `tag:gateway,tag:proxy`)
- `TS_IMAGE`: Tailscale daemon image (default `tailscale/tailscale:latest`)
- `TS_AUTHKEY` (optional/legacy): If set, used directly; otherwise an auth key is generated automatically
- `TS_CERT_DOMAIN` (optional): DNS suffix used for certificates

The controller reads these via the `internal/config` package. Tags are parsed from `TAILSCALE_TAGS` and default to `tag:gateway` when unset.

## Auth Key Handling

- The controller checks for a Secret named `<gateway-name>` in the Gateway namespace.
- If the Secret contains `authkey`, it is left unchanged.
- If missing, the controller generates a non-reusable, ephemeral, preauthorized auth key using the official Tailscale client (`tailscale.com/client/tailscale/v2`).
- Tags for the key are sourced from `TAILSCALE_TAGS`.

Security note: Prefer OAuth-based key generation over storing reusable keys. If you must provide a key, inject via `TS_AUTHKEY` and avoid committing secrets to source control.

## Reconciliation Flow

- Listener validation for supported protocols and ports
- HTTPRoute discovery bound to the Gateway
- Tailscale Serve configuration build and ConfigMap creation (`services.hujson`)
- Resource reconciliation:
  - ServiceAccount
  - RBAC (ClusterRoleBinding)
  - Secret (auth key)
  - ConfigMap (serve config)
  - DaemonSet (pods run Tailscale with postStart drain/advertise lifecycle)
- Resource application uses server-side apply
- Parallelization: Independent resources (SA/RBAC/Secret/ConfigMap/DaemonSet) run concurrently
- Status updated to Ready upon success, including a hostname `<namespace>-<name>`

## Running Locally

- Set environment variables:

```
export TAILSCALE_TAILNET=example.com
export TAILSCALE_OAUTH_CLIENT_ID=...
export TAILSCALE_OAUTH_CLIENT_SECRET=...
export TAILSCALE_TAGS="tag:gateway"
```

- Build: `go build ./...`
- Deploy the controller to your cluster and create a Gateway with `gatewayClassName: tailscale`.

## Migration Notes

- Tags env renamed from `TAILSCALE_AUTH_TAGS` to `TAILSCALE_TAGS`.
- Secret generation moved behind helpers for clarity:
  - `tailscaleConfigData` for Secret `stringData`
  - `tailscaleServicesConfig` for ConfigMap data
- Aggregated resource reconciliation via `ReconcilerResources`.

## References

- Tailscale client for API: https://github.com/tailscale/tailscale-client-go-v2
- OAuth clients and scopes: https://tailscale.com/kb/1215/oauth-clients
