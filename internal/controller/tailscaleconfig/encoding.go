package tailscaleconfig

import (
	"encoding/json"

	"github.com/tailscale/hujson"
)

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
