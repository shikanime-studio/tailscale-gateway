// Package tsclient provides a thin wrapper around the Tailscale API client
// for generating auth keys and managing devices.
package tsclient

import (
	"context"
	"fmt"

	"tailscale.com/client/tailscale/v2"
)

// TailscaleClient wraps the Tailscale API client and configuration to perform
// auth key creation and device management operations.
type TailscaleClient struct {
	c   *tailscale.Client
	cfg interface {
		GetTailscaleOAuthClientID() string
		GetTailscaleOAuthClientSecret() string
		GetTailscaleKeyDescription() string
	}
}

// New creates a TailscaleClient using OAuth credentials from the provided Config.
// It returns an error if required OAuth configuration is missing.
func New(cfg interface {
	GetTailscaleOAuthClientID() string
	GetTailscaleOAuthClientSecret() string
	GetTailscaleKeyDescription() string
}) (*TailscaleClient, error) {
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

// DeleteDevice deletes the device with the provided ID.
func (tc *TailscaleClient) DeleteDevice(ctx context.Context, id string) error {
	return tc.c.Devices().
		Delete(ctx, id)
}
