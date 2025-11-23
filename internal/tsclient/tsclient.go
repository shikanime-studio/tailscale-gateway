package tsclient

import (
	"context"
	"fmt"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	ts "tailscale.com/client/tailscale/v2"
)

type TailscaleClient struct{ c *ts.Client }

func New(cfg *config.Config) (*TailscaleClient, error) {
	tailnet := cfg.GetTailscaleTailnet()
	clientID := cfg.GetTailscaleOAuthClientID()
	clientSecret := cfg.GetTailscaleOAuthClientSecret()
	if tailnet == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("missing required OAuth configuration")
	}
	c := &ts.Client{
		Tailnet: tailnet,
		Auth: &ts.OAuth{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       []string{"all:write"},
		},
	}
	return &TailscaleClient{c: c}, nil
}

func (tc *TailscaleClient) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	var caps ts.KeyCapabilities
	caps.Devices.Create.Reusable = false
	caps.Devices.Create.Ephemeral = true
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = tags
	key, err := tc.c.Keys().
		Create(ctx, ts.CreateKeyRequest{Capabilities: caps, Description: "tailscale-gateway-proxy"})
	if err != nil {
		return "", err
	}
	return key.Key, nil
}
