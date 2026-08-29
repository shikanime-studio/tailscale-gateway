<!-- owner: shikanime | zone: internal | purpose: install, deploy, release, branch protection -->

# Runbook

## Install (first time)

1. Gateway API CRDs:

   ```sh
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml
   kubectl get crd gateways.gateway.networking.k8s.io httproutes.gateway.networking.k8s.io
   ```

2. Controller Secret in `tailscale-system`:

   ```sh
   kubectl create namespace tailscale-system
   kubectl -n tailscale-system create secret generic \
     tailscale-gateway-controller \
     --from-literal=TAILSCALE_OAUTH_CLIENT_ID=<client-id> \
     --from-literal=TAILSCALE_OAUTH_CLIENT_SECRET=<client-secret>
   ```

3. Controller + GatewayClass:

   ```sh
   kubectl apply -k https://github.com/shikanime-studio/tailscale-gateway/manifests/gateway
   kubectl -n tailscale-system get deploy/tailscale-gateway-controller,svc/tailscale-gateway-controller-metrics
   kubectl get gatewayclass tailscale
   ```

## Expose services

Via a `Gateway` + `HTTPRoute`:

```sh
kubectl apply -k ./manifests/demo
kubectl -n tailscale-gateway-demo get gateway demo httproute demo
```

Via a `Service`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    tailscale.gateway.shikanime.studio/hostname: demo.example.com
spec:
  type: LoadBalancer
  loadBalancerClass: tailscale
```

## Release

- Tagged from `main`; tags `vX.Y.Z` are published from CI.
- Bump the controller image / overlay only through a reviewed PR.

## Branch protection

`main` requires: 1 approving review, linear history (no merge commits), signed
commits, squash+rebase merge only.
