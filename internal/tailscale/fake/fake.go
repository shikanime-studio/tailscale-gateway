package fake

import (
	"context"
)

// FakeClient is a fake implementation of tailscale.Interface for testing.
type FakeClient struct {
	CreateAuthKeyFunc func(ctx context.Context, tags []string) (string, error)
	DeleteDeviceFunc  func(ctx context.Context, id string) error
}

func (m *FakeClient) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	if m.CreateAuthKeyFunc != nil {
		return m.CreateAuthKeyFunc(ctx, tags)
	}
	return "tskey-auth-mock", nil
}

func (m *FakeClient) DeleteDevice(ctx context.Context, id string) error {
	if m.DeleteDeviceFunc != nil {
		return m.DeleteDeviceFunc(ctx, id)
	}
	return nil
}
