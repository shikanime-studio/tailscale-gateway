// Package config provides controller configuration loaded from environment variables.
package config

import (
	"github.com/spf13/viper"
)

// Config wraps application configuration and environment bindings.
type Config struct{ v *viper.Viper }

// New constructs a new Config with defaults and environment bindings.
func New() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("metrics_bind_address", ":8080")
	v.SetDefault("health_probe_bind_address", ":8081")
	v.SetDefault("tailscale_image", "tailscale/tailscale:latest")
	v.SetDefault("tailscale_tailnet", "")
	v.SetDefault("caddy_image", "caddy:latest")
	v.SetDefault("tailscale_cert_domain", "")

	if err := v.BindEnv("metrics_bind_address", "METRICS_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("health_probe_bind_address", "HEALTH_PROBE_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("tailscale_image", "TAILSCALE_IMAGE"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("tailscale_authkey", "TAILSCALE_AUTHKEY"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("tailscale_tailnet", "TAILSCALE_TAILNET"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("caddy_image", "CADDY_IMAGE"); err != nil {
		return nil, err
	}

	return &Config{v: v}, nil
}

// GetMetricsBindAddress returns the metrics server bind address.
func (c *Config) GetMetricsBindAddress() string { return c.v.GetString("metrics_bind_address") }

// GetHealthProbeBindAddress returns the health probe bind address.
func (c *Config) GetHealthProbeBindAddress() string {
	return c.v.GetString("health_probe_bind_address")
}

// GetTailscaleAPIKey returns the optional Tailscale API key.
func (c *Config) GetTailscaleAPIKey() string { return c.v.GetString("tailscale_authkey") }

// GetTailscaleImage returns the tailscale daemon container image.
func (c *Config) GetTailscaleImage() string { return c.v.GetString("tailscale_image") }

// GetTailscaleTailnet returns the certificate DNS name.
func (c *Config) GetTailscaleTailnet() string { return c.v.GetString("tailscale_tailnet") }

// GetCaddyImage returns the caddy reverse proxy container image.
func (c *Config) GetCaddyImage() string { return c.v.GetString("caddy_image") }
