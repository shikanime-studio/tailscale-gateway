# Tailscale Gateway API Controller

A Kubernetes controller that implements the Gateway API for Tailscale integration, providing secure ingress capabilities through Tailscale's mesh network.

## Overview

This controller manages Gateway API resources and creates Tailscale proxy servers to route traffic from the Tailscale network to Kubernetes services. It supports:

- **Gateway API Compliance**: Full implementation of Gateway API v1 specifications
- **HTTPRoute Integration**: Automatic discovery and routing of HTTPRoutes
- **Load Balancing**: Round-robin load balancing across multiple backend services
- **Health Checking**: Automatic health monitoring of backend services
- **Status Updates**: Comprehensive status reporting and condition management
- **High Availability**: Support for multiple replicas and failover capabilities

## Features

### Core Functionality

- ✅ Gateway API v1 support
- ✅ HTTPRoute parent reference handling
- ✅ Multi-backend load balancing
- ✅ Health-based backend selection
- ✅ Automatic status updates
- ✅ Comprehensive error handling

### Advanced Features

- 🔒 Secure Tailscale integration
- 📊 Health monitoring and failover
- 🔄 Dynamic backend updates
- 📝 Detailed logging and observability
- 🚀 High-performance proxy implementation

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   Gateway API   │────▶│  Tailscale       │────▶│   Kubernetes    │
│   Controller    │     │  Proxy Server    │     │   Services      │
└─────────────────┘     └──────────────────┘     └─────────────────┘
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   Gateway       │     │   Health         │     │   HTTPRoute     │
│   Status        │◀────│   Checker        │◀────│   Discovery     │
└─────────────────┘     └──────────────────┘     └─────────────────┘
```

## Installation

### Prerequisites

- Kubernetes cluster (v1.19+)
- Tailscale account and auth key
- Gateway API CRDs installed
- kubectl configured

### Quick Start

1. **Install Gateway API CRDs**:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml
```

2. **Create GatewayClass**:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: tailscale
spec:
  controllerName: tailscale.io/gateway-controller
```

3. **Deploy the Controller**:

```bash
# Build and deploy the controller
make deploy TS_AUTHKEY=your-tailscale-auth-key
```

## Configuration

### Gateway Configuration

Create a Gateway resource to expose your services:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: example-gateway
  namespace: default
spec:
  gatewayClassName: tailscale
  listeners:
    - name: http
      protocol: HTTP
      port: 80
```

### HTTPRoute Configuration

Create HTTPRoutes to define routing rules:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: example-route
  namespace: default
spec:
  parentRefs:
    - name: example-gateway
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: example-service
          port: 80
```

### Annotations

Use annotations for additional configuration:

```yaml
metadata:
  annotations:
    tailscale.com/backend: "http://fallback-service:8080" # Fallback backend
    tailscale.com/ephemeral: "true" # Ephemeral node
    tailscale.com/tags: "tag:k8s,tag:prod" # Tailscale tags
```

## Usage

### Basic Example

1. **Create a sample application**:

```bash
kubectl apply -f examples/sample-app.yaml
```

2. **Create Gateway and HTTPRoute**:

```bash
kubectl apply -f examples/gateway.yaml
kubectl apply -f examples/httproute.yaml
```

3. **Verify deployment**:

```bash
kubectl get gateway example-gateway -o yaml
kubectl get httproute example-route -o yaml
```

### Advanced Configuration

#### Multiple Backends

The controller automatically discovers multiple backends from HTTPRoutes and implements round-robin load balancing:

```yaml
spec:
  rules:
    - backendRefs:
        - name: service-v1
          port: 80
          weight: 90
        - name: service-v2
          port: 80
          weight: 10
```

#### Health Checking

Backends are automatically health-checked every 30 seconds. Unhealthy backends are removed from the rotation until they recover.

#### Status Monitoring

Monitor Gateway status:

```bash
kubectl describe gateway example-gateway
```

Example output:

```
Status:
  Addresses:
    Type:   Hostname
    Value:  default-example-gateway.ts.net
  Conditions:
    Last Transition Time:  2024-01-01T00:00:00Z
    Message:               Gateway is ready
    Reason:                Ready
    Status:                True
    Type:                  Ready
  Listeners:
    Conditions:
      Last Transition Time:  2024-01-01T00:00:00Z
      Message:               Listener is ready
      Reason:                Ready
      Status:                True
      Type:                  Ready
    Name:                    http
```

## Development

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Running Locally

```bash
# Run with local Kubernetes config
make run TS_AUTHKEY=your-auth-key
```

## Monitoring

### Metrics

The controller exposes Prometheus metrics on `:8080/metrics`:

- `gateway_reconcile_total`: Total number of Gateway reconciliations
- `gateway_reconcile_errors_total`: Number of reconciliation errors
- `gateway_ready_status`: Current ready status of Gateways
- `backend_health_status`: Health status of backend services

### Logging

Configure log level with the `--zap-log-level` flag:

```bash
--zap-log-level=debug
```

## Troubleshooting

### Common Issues

1. **Gateway not becoming ready**:
   - Check HTTPRoute parent references
   - Verify backend services are healthy
   - Check controller logs for errors

2. **Tailscale connection issues**:
   - Verify auth key is valid
   - Check Tailscale network connectivity
   - Review Tailscale node status

3. **Backend routing problems**:
   - Verify service names and ports
   - Check health check endpoints
   - Review proxy server logs

### Debug Commands

```bash
# Check controller logs
kubectl logs -n tailscale-system deployment/gateway-controller

# Check Gateway status
kubectl get gateway -o yaml

# Check HTTPRoute status
kubectl get httproute -o yaml

# Test connectivity
kubectl run debug --image=curlimages/curl --rm -it -- curl http://your-service
```

## API Reference

### Gateway Conditions

| Condition   | Description                                |
| ----------- | ------------------------------------------ |
| `Ready`     | Gateway is configured and operational      |
| `Scheduled` | Gateway has been scheduled to a controller |

### Listener Conditions

| Condition      | Description                       |
| -------------- | --------------------------------- |
| `Ready`        | Listener is configured and ready  |
| `ResolvedRefs` | All references have been resolved |

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Support

- 📧 Email: support@tailscale.com
- 💬 Slack: [#tailscale-gateway](https://tailscale.slack.com)
- 🐛 Issues: [GitHub Issues](https://github.com/infinity-blackhole/tailscale-gateway-api/issues)
