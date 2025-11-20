# Tailscale Gateway API Controller

Kubernetes controller that integrates the Gateway API with Tailscale. For each `Gateway`, it provisions a per-node proxy DaemonSet that bridges Tailscale Serve to cluster `Service`s discovered from `HTTPRoute`s.

## Overview

- Reconciles `Gateway` resources and discovers referenced `HTTPRoute`s
- Generates Tailscale Serve HUJSON config and a Caddy reverse_proxy config
- Applies `ConfigMap`s and a DaemonSet with `tailscale` + `caddy` containers
- Updates `Gateway` status and listener conditions; publishes a hostname address

## Features

- Gateway API v1: HTTP/HTTPS listeners
- HTTPRoute backend discovery across namespaces via `parentRefs`
- Reverse proxy to `service.namespace:port` endpoints via Caddy
- Simple round‑robin balancing across multiple backends
- Status conditions for Gateway and Listeners; hostname `ns-name.ts.net` when ready

## Install

### Prerequisites

- A Kubernetes cluster and `kubectl`
- Gateway API CRDs (`v1.0.0`)
- Tailscale account and auth key

### Option A: Kustomize

```bash
kubectl apply -k manifests/gateway/base

# Create controller auth key Secret in tailscale-system
kubectl -n tailscale-system create secret generic tailscale-gateway-controller \
  --from-literal=authkey=tskey-xxxxxxxxxxxxxxxx
```

### Option B: Skaffold (ko)

```bash
# Requires ko and skaffold
skaffold dev -p demo
```

The demo profile builds the controller image with ko and deploys `manifests/gateway/overlays/demo`, which sets `TS_CERT_BASE_DOMAIN` for generated hostnames.

## Configuration

Environment variables consumed by the controller:

- `METRICS_BIND_ADDRESS` (default `:8080`)
- `HEALTH_PROBE_BIND_ADDRESS` (default `:8081`)
- `PROXY_IMAGE` (default `caddy:latest`)
- `TAILSCALE_IMAGE` (default `tailscale/tailscale:latest`)
- `TS_AUTHKEY` (optional; controller reads and writes `authkey` in Secret)
- `TS_CERT_BASE_DOMAIN` (optional; suffix appended to generated hostnames)

## Usage

### Define Gateway and HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: demo
  namespace: tailscale-gateway-demo
spec:
  gatewayClassName: tailscale
  listeners:
    - name: http
      protocol: HTTP
      port: 80
    - name: https
      protocol: HTTPS
      port: 443
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: demo
  namespace: tailscale-gateway-demo
spec:
  parentRefs:
    - name: demo
  hostnames:
    - demo
  rules:
    - backendRefs:
        - name: demo
          port: 80
```

Apply the demo app and routes:

```bash
kubectl apply -k manifests/demo
```

### What gets created

- DaemonSet `<gateway>-tailscale-gateway` in the Gateway namespace
- ConfigMap `<gateway>-services` containing `services.hujson`
- ConfigMap `<gateway>-caddy-config` containing `Caddyfile`
- Secret `<gateway>` in the Gateway namespace with key `authkey` (populated if `TS_AUTHKEY` is set)

## Observability

- Metrics: `:8080/metrics` (Service `tailscale-gateway-controller-metrics`)
- Health/Ready: `:8081` HTTP probes
- Logging: zap in dev mode

## Troubleshooting

```bash
# Controller logs
kubectl -n tailscale-system logs deploy/tailscale-gateway-controller

# Gateway and Listener conditions
kubectl -n <ns> get gateway <name> -o yaml

# DaemonSet and pods
kubectl -n <ns> get ds,pods -l app=tailscale-gateway

# Configs
kubectl -n <ns> get cm <name>-caddy-config <name>-services -o yaml
```

## Development

```bash
# Build binary
go build ./cmd/controller

# Run tests
go test ./...

# Build and deploy demo with ko + skaffold
skaffold dev -p demo
```
