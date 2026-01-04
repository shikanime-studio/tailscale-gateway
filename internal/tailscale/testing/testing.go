// Package testing provides a fake implementation of the Tailscale client for testing.
package testing

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
)

// Client is a fake implementation of tailscale.Interface for testing.
type Client struct {
	mu             sync.Mutex
	AuthKeys       map[string]struct{}
	DeletedDevices map[string]struct{}

	rng *rand.Rand
}

// New creates a new fake Client with the given random source.
func New(src rand.Source) *Client {
	return &Client{
		AuthKeys:       make(map[string]struct{}),
		DeletedDevices: make(map[string]struct{}),
		rng:            rand.New(src),
	}
}

// CreateAuthKey simulates creating an auth key.
func (c *Client) CreateAuthKey(ctx context.Context, tags []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Generate a random key
	key := fmt.Sprintf("tskey-auth-%d", c.rng.Int63())
	c.AuthKeys[key] = struct{}{}
	return key, nil
}

// DeleteDevice simulates deleting a device.
func (c *Client) DeleteDevice(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.DeletedDevices[id] = struct{}{}
	return nil
}
