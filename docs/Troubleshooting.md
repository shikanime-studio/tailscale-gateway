<!-- owner: shikanime | zone: internal | purpose: known failure modes and fixes -->

# Troubleshooting

## Gateway never becomes Ready

- Check the controller pod logs:
  `kubectl -n tailscale-system logs deploy/tailscale-gateway-controller`.
- Confirm the `tailscale-gateway-controller` Secret exists and the OAuth client
  ID/secret are valid and have key-creation scopes.
- Confirm the `tailscale` `GatewayClass` exists and is Accepted.

## Auth key errors

- The controller generates an ephemeral preauth key unless a Secret named
  `<gateway-name>` in the Gateway namespace contains `authkey`. If you supply
  `authkey`, it is reused as-is — a bad/expired key will fail reconciliation.
  Rotate it in the Secret.
- `TAILSCALE_TAGS` drives device tags; unset defaults to `tag:gateway`. A tag
  the OAuth client cannot apply will surface as a Tailscale API error.

## Service not exposed

- `spec.type` must be `LoadBalancer` and `spec.loadBalancerClass` must be
  `tailscale`.
- The hostname annotation key is `tailscale.gateway.shikanime.studio/hostname`.
  A typo falls back to no exposure.

## e2e flaky / green in CI

- The e2e suite is behind the `e2e` build tag; `go test ./...` intentionally
  skips it. Run it explicitly (see Development.md) against a real cluster.

## Image override ignored

- Set `TAILSCALE_IMAGE` in the controller Secret; default is
  `tailscale/tailscale:latest`.
