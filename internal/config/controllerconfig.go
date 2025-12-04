package config

import (
	"strings"

	"github.com/spf13/viper"
)

type ControllerConfig struct{ v *viper.Viper }

func NewControllerConfig() (*ControllerConfig, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("metrics_bind_address", ":8080")
	v.SetDefault("health_probe_bind_address", ":8081")
	v.SetDefault("ts_image", "ghcr.io/shikanime-studio/tailscale-gateway/proxy:latest")
	v.SetDefault("ts_tags", "")
	v.SetDefault("ts_oauth_client_id", "")
	v.SetDefault("ts_oauth_client_secret", "")
	v.SetDefault("ts_key_description", "Ephemeral auth key for gateway provisioning")

	if err := v.BindEnv("metrics_bind_address", "METRICS_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("health_probe_bind_address", "HEALTH_PROBE_BIND_ADDRESS"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_image", "TAILSCALE_IMAGE"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_tags", "TAILSCALE_TAGS", "ts_tags"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_oauth_client_id", "TAILSCALE_OAUTH_CLIENT_ID", "ts_oauth_client_id"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_oauth_client_secret", "TAILSCALE_OAUTH_CLIENT_SECRET", "ts_oauth_client_secret"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_key_description", "TAILSCALE_KEY_DESCRIPTION", "ts_key_description"); err != nil {
		return nil, err
	}

	return &ControllerConfig{v: v}, nil
}

func (c *ControllerConfig) GetMetricsBindAddress() string {
	return c.v.GetString("metrics_bind_address")
}
func (c *ControllerConfig) GetHealthProbeBindAddress() string {
	return c.v.GetString("health_probe_bind_address")
}
func (c *ControllerConfig) GetTailscaleImage() string { return c.v.GetString("ts_image") }
func (c *ControllerConfig) GetTailscaleOAuthClientID() string {
	return c.v.GetString("ts_oauth_client_id")
}
func (c *ControllerConfig) GetTailscaleOAuthClientSecret() string {
	return c.v.GetString("ts_oauth_client_secret")
}
func (c *ControllerConfig) GetTailscaleKeyDescription() string {
	return c.v.GetString("ts_key_description")
}

func (c *ControllerConfig) GetTailscaleTags() []string {
	v := c.v.GetString("ts_tags")
	if v == "" {
		return []string{"tag:gateway"}
	}
	parts := strings.Split(v, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tags = append(tags, p)
		}
	}
	if len(tags) == 0 {
		return []string{"tag:gateway"}
	}
	return tags
}
