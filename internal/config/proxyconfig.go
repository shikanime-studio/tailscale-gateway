package config

import (
	"github.com/spf13/viper"
)

type ProxyConfig struct {
	v *viper.Viper
}

func NewProxyConfig() (*ProxyConfig, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("addr", ":80")
	v.SetDefault("hostname", "tshello")
	v.SetDefault("ts_serve_config", "/etc/tailscaled/services.hujson")
	v.SetDefault("ts_authkey", "")
	v.SetDefault("ts_dir", "/var/lib/tailscale")

	if err := v.BindEnv("addr", "ADDR"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("hostname", "HOSTNAME"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_serve_config", "TS_SERVE_CONFIG"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_authkey", "TS_AUTHKEY"); err != nil {
		return nil, err
	}
	if err := v.BindEnv("ts_dir", "TS_DIR"); err != nil {
		return nil, err
	}

	return &ProxyConfig{v: v}, nil
}

func (c *ProxyConfig) GetProxyAddr() string     { return c.v.GetString("addr") }
func (c *ProxyConfig) GetProxyHostname() string { return c.v.GetString("hostname") }

func (c *ProxyConfig) GetTailscaleServeConfigPath() string { return c.v.GetString("ts_serve_config") }
func (c *ProxyConfig) GetTailscaleAuthKey() string         { return c.v.GetString("ts_authkey") }
func (c *ProxyConfig) GetTailscaleDir() string             { return c.v.GetString("ts_dir") }
