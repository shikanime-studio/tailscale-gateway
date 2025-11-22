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
	v.SetDefault("ts_image", "tailscale/tailscale:latest")
	v.SetDefault("ts_cert_domain", "")

	if err := v.BindEnv("metrics_bind_address", "METRICS_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("health_probe_bind_address", "HEALTH_PROBE_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_image", "TS_IMAGE"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_auth_key", "TS_AUTHKEY", "ts_auth_key"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_cert_domain", "TS_CERT_DOMAIN", "ts_cert_domain"); err != nil {
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

// GetTailscaleAuthKey returns the optional Tailscale auth key.
func (c *Config) GetTailscaleAuthKey() string { return c.v.GetString("ts_auth_key") }

// GetTailscaleImage returns the tailscale daemon container image.
func (c *Config) GetTailscaleImage() string { return c.v.GetString("ts_image") }

// GetTailscaleCertDomain returns the optional DNS suffix to append for certificates.
func (c *Config) GetTailscaleCertDomain() string { return c.v.GetString("ts_cert_domain") }
