# Tailscale Gateway Controller

A Kubernetes controller that provisions a Tailscale-based Gateway.

## Installation

### Prerequisites

- A Kubernetes cluster with access to create cluster-scoped resources
- Gateway API CRDs (`gateway.networking.k8s.io/v1`)
- Tailscale OAuth client credentials with scopes that allow key creation

### Install Gateway API CRDs

Ensure the Gateway API CRDs are installed. If not present, install a stable
release:

```shell
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml
```

Verify:

```shell
kubectl get crd gateways.gateway.networking.k8s.io httproutes.gateway.networking.k8s.io
```

### Configure Controller Secrets

Create the Secret the controller consumes for environment configuration in the `tailscale-system` namespace:

```shell
kubectl create namespace tailscale-system
kubectl -n tailscale-system create secret generic tailscale-gateway-controller \
  --from-literal=TAILSCALE_OAUTH_CLIENT_ID=<client-id> \
  --from-literal=TAILSCALE_OAUTH_CLIENT_SECRET=<client-secret>
```

Optional values:

- `TAILSCALE_IMAGE`: Override the Tailscale daemon image used by the gateway
  pods

See Environment Configuration below for details on each variable.

### Deploy the Controller and GatewayClass

Apply the provided Kustomize overlay to install the controller, RBAC,
ServiceAccount, metrics Service, and the `GatewayClass` named `tailscale`:

```shell
kubectl apply -k https://github.com/shikanime-studio/tailscale-gateway/manifests/gateway
```

Check status:

```shell
kubectl -n tailscale-system get deploy/tailscale-gateway-controller,svc/tailscale-gateway-controller-metrics
kubectl get gatewayclass tailscale
```

### Create a Gateway and HTTPRoutes

With the controller running, create a `Gateway` bound to the `tailscale`
`GatewayClass` and `HTTPRoute`s. Example manifests are provided:

```shell
kubectl apply -k ./manifests/demo
kubectl -n tailscale-gateway-demo get gateway demo
kubectl -n tailscale-gateway-demo get httproute demo
```

When reconciliation succeeds, the `Gateway` status reports Ready and the
controller.

## Environment Configuration

- `TAILSCALE_OAUTH_CLIENT_ID`: OAuth client ID with scopes allowing key creation
- `TAILSCALE_OAUTH_CLIENT_SECRET`: OAuth client secret
- `TAILSCALE_TAGS`: Comma-separated device tags applied to generated auth keys
  (e.g. `tag:gateway,tag:proxy`)
- `TAILSCALE_IMAGE`: Tailscale daemon image (default
  `tailscale/tailscale:latest`)

The controller reads these via the `internal/config` package. Tags are parsed
from `TAILSCALE_TAGS` and default to `tag:gateway` when unset.

## Auth Key Handling

- The controller checks for a Secret named `<gateway-name>` in the Gateway
  namespace.
- If the Secret contains `authkey`, it is left unchanged.
- If missing, the controller generates a non-reusable, ephemeral, preauthorized
  auth key using the official Tailscale client
  (`tailscale.com/client/tailscale/v2`).
- Tags for the key are sourced from `TAILSCALE_TAGS`.

## References

- Tailscale client for API: https://github.com/tailscale/tailscale-client-go-v2
- OAuth clients and scopes: https://tailscale.com/kb/1215/oauth-clients
