package controller

import (
	"github.com/spf13/viper"
)

// Config wraps application configuration and environment bindings.
type Config struct {
	v *viper.Viper
}

// New constructs a new Config with defaults and environment bindings.
func New() *Config {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("metrics_bind_address", ":8080")
	v.SetDefault("health_probe_bind_address", ":8081")
	v.SetDefault("proxy_image", "caddy:latest")
	v.SetDefault("tailscale_image", "tailscale/tailscale:latest")

	v.BindEnv("metrics_bind_address", "METRICS_BIND_ADDRESS")
	v.BindEnv("health_probe_bind_address", "HEALTH_PROBE_BIND_ADDRESS")
	v.BindEnv("proxy_image", "PROXY_IMAGE")
	v.BindEnv("tailscale_image", "TAILSCALE_IMAGE")
	v.BindEnv("ts_auth_key", "TS_AUTHKEY", "ts_auth_key")

	return &Config{v: v}
}

// GetMetricsBindAddress returns the metrics server bind address.
func (c *Config) GetMetricsBindAddress() string { return c.v.GetString("metrics_bind_address") }

// GetHealthProbeBindAddress returns the health probe bind address.
func (c *Config) GetHealthProbeBindAddress() string {
	return c.v.GetString("health_probe_bind_address")
}

// GetTSAuthKey returns the optional Tailscale auth key.
func (c *Config) GetTSAuthKey() string { return c.v.GetString("ts_auth_key") }

// GetProxyImage returns the container image for the proxy sidecar.
func (c *Config) GetProxyImage() string { return c.v.GetString("proxy_image") }

// GetTailscaleImage returns the tailscale daemon container image.
func (c *Config) GetTailscaleImage() string { return c.v.GetString("tailscale_image") }
