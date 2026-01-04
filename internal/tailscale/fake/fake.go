// Package fake provides a fake implementation of the Tailscale client for testing.
package fake

import (
	"context"
)

// Client is a fake implementation of tailscale.Interface for testing.
type Client struct {
	CreateAuthKeyFunc func(ctx context.Context, tags []string) (string, error)
	DeleteDeviceFunc  func(ctx context.Context, id string) error
}

// CreateAuthKey simulates creating an auth key.
func (m *Client) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	if m.CreateAuthKeyFunc != nil {
		return m.CreateAuthKeyFunc(ctx, tags)
	}
	return "tskey-auth-mock", nil
}

// DeleteDevice simulates deleting a device.
func (m *Client) DeleteDevice(ctx context.Context, id string) error {
	if m.DeleteDeviceFunc != nil {
		return m.DeleteDeviceFunc(ctx, id)
	}
	return nil
}
