// Package testing provides a fake implementation of the Tailscale client for testing.
package testing

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
)

// AuthKey represents an auth key in the fake Tailscale client.
type AuthKey struct {
	Tags []string
}

// Client is a fake implementation of tailscale.Interface for testing.
type Client struct {
	mu             sync.Mutex
	AuthKeys       map[string]AuthKey
	DeletedDevices map[string]struct{}

	rng *rand.Rand
}

// New creates a new fake Client with the given random source.
func New(src rand.Source) *Client {
	return &Client{
		AuthKeys:       make(map[string]AuthKey),
		DeletedDevices: make(map[string]struct{}),
		rng:            rand.New(src),
	}
}

// CreateAuthKey simulates creating an auth key.
func (c *Client) CreateAuthKey(_ context.Context, tags []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Generate a random key
	key := fmt.Sprintf("tskey-auth-%d", c.rng.Int63())
	c.AuthKeys[key] = AuthKey{
		Tags: tags,
	}
	return key, nil
}

// DeleteDevice simulates deleting a device.
func (c *Client) DeleteDevice(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.DeletedDevices[id] = struct{}{}
	return nil
}
