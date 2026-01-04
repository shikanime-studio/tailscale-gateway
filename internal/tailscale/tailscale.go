// Package tailscale provides a wrapper around the Tailscale API client.
package tailscale

import (
	"context"
	"fmt"

	ts "tailscale.com/client/tailscale/v2"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
)

// Interface defines the interface for Tailscale client operations.
type Interface interface {
	CreateAuthKey(ctx context.Context, tags []string) (string, error)
	DeleteDevice(ctx context.Context, id string) error
}

// Client wraps the Tailscale API client and configuration to perform
// auth key creation and device management operations.
type Client struct {
	c   *ts.Client
	cfg *config.Config
}

// New creates a Client using OAuth credentials from the provided Config.
// It returns an error if required OAuth configuration is missing.
func New(cfg *config.Config) (*Client, error) {
	clientID := cfg.GetTailscaleOAuthClientID()
	clientSecret := cfg.GetTailscaleOAuthClientSecret()
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("missing required OAuth configuration")
	}
	c := &ts.Client{
		Auth: &ts.OAuth{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       []string{"all:write"},
		},
	}
	return &Client{c: c, cfg: cfg}, nil
}

// CreateAuthKey generates an ephemeral, preauthorized auth key scoped to the
// provided tags and returns the key string.
func (tc *Client) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	var caps ts.KeyCapabilities
	caps.Devices.Create.Reusable = false
	caps.Devices.Create.Ephemeral = true
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = tags
	desc := tc.cfg.GetTailscaleKeyDescription()
	ref := ts.CreateKeyRequest{
		Capabilities: caps,
		Description:  desc,
	}
	key, err := tc.c.Keys().CreateAuthKey(ctx, ref)
	if err != nil {
		return "", err
	}
	return key.Key, nil
}

// DeleteDevice deletes the device with the provided ID.
func (tc *Client) DeleteDevice(ctx context.Context, id string) error {
	return tc.c.Devices().
		Delete(ctx, id)
}
