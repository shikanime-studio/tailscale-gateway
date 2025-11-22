package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	tailscale "tailscale.com/client/tailscale/v2"
)

// NetworkManager wraps interactions with the Tailscale control plane for
// network-level resource management tied to a Gateway.
type NetworkManager struct {
	client *tailscale.Client
}

// NewNetworkManager constructs a NetworkManager using configuration values.
func NewNetworkManager(cfg *config.Config) *NetworkManager {
	return &NetworkManager{client: &tailscale.Client{Tailnet: cfg.GetTailscaleTailnet(), APIKey: cfg.GetTailscaleAPIKey()}}
}

// DeleteDevices removes Tailscale devices belonging to the given Gateway,
// matched by the hostname prefix "<namespace>-<name>-".
func (n *NetworkManager) DeleteDevices(ctx context.Context, gw *gatewayv1.Gateway) error {
	if n.client.APIKey == "" || n.client.Tailnet == "" {
		return nil
	}
	devices, err := n.client.Devices().List(ctx)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("%s-%s-", gw.Namespace, gw.Name)
	for _, d := range devices {
		if strings.HasPrefix(d.Hostname, prefix) {
			if err := n.client.Devices().Delete(ctx, d.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
