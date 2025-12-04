// Package tsconfig builds and encodes Tailscale serve configurations.
package tsconfig

import (
	"encoding/json"
	"os"

	"github.com/tailscale/hujson"
	"tailscale.com/ipn"
)

// Marshal serializes the Config to HUJSON-formatted Tailscale serve config bytes.
func Marshal(cfg *Config) ([]byte, error) {
	b, err := json.Marshal(cfg.cfg)
	if err != nil {
		return nil, err
	}
	fb, err := hujson.Format(b)
	if err != nil {
		return nil, err
	}
	return fb, nil
}

// Unmarshal parses HUJSON-formatted Tailscale serve config bytes into Config.
func Unmarshal(b []byte) (*Config, error) {
	// Parse HUJSON and convert to strict JSON
	p, err := hujson.Parse(b)
	if err != nil {
		return nil, err
	}
	p.Standardize()
	jb := p.Pack()
	var sc ipn.ServeConfig
	if err := json.Unmarshal(jb, &sc); err != nil {
		return nil, err
	}
	return &Config{cfg: &sc}, nil
}

// ParseFile reads a HUJSON config file from path and returns Config.
func ParseFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Unmarshal(b)
}
