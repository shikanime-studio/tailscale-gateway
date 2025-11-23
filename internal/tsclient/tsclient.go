package tsclient

import (
	"context"
	"fmt"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"tailscale.com/client/tailscale/v2"
)

type TailscaleClient struct {
	c   *tailscale.Client
	cfg *config.Config
}

// New creates a TailscaleClient using OAuth credentials from the provided Config.
// It returns an error if required OAuth configuration is missing.
func New(cfg *config.Config) (*TailscaleClient, error) {
	clientID := cfg.GetTailscaleOAuthClientID()
	clientSecret := cfg.GetTailscaleOAuthClientSecret()
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("missing required OAuth configuration")
	}
	c := &tailscale.Client{
		Auth: &tailscale.OAuth{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       []string{"all:write"},
		},
	}
	return &TailscaleClient{c: c, cfg: cfg}, nil
}

// CreateAuthKey generates an ephemeral, preauthorized auth key scoped to the
// provided tags and returns the key string.
func (tc *TailscaleClient) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	var caps tailscale.KeyCapabilities
	caps.Devices.Create.Reusable = false
	caps.Devices.Create.Ephemeral = true
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = tags
	desc := tc.cfg.GetTailscaleKeyDescription()
	ref := tailscale.CreateKeyRequest{
		Capabilities: caps,
		Description:  desc,
	}
	key, err := tc.c.Keys().
		Create(ctx, ref)
	if err != nil {
		return "", err
	}
	return key.Key, nil
}
