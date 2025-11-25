package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/shikanime-studio/tailscale-gateway/internal/tsconfig"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

type ProxyConfig struct {
	Addr       string
	Hostname   string
	ConfigPath string
	s          tsnet.Server
	ln         net.Listener
	lc         *local.Client
}

func (p *ProxyConfig) start() error {
	_ = os.Setenv("TS_SERVE_CONFIG", p.ConfigPath)
	p.s = tsnet.Server{Hostname: p.Hostname}
	ln, err := p.s.Listen("tcp", p.Addr)
	if err != nil {
		return err
	}
	p.ln = ln
	lc, err := p.s.LocalClient()
	if err != nil {
		return err
	}
	p.lc = lc
	return nil
}

func (p *ProxyConfig) advertise(cfg *tsconfig.Config) error {
	var adv []string
	for _, n := range cfg.AdvertisedServices() {
		adv = append(adv, string(n))
	}
	_, err := p.lc.EditPrefs(context.Background(), &ipn.MaskedPrefs{AdvertiseServicesSet: true, Prefs: ipn.Prefs{AdvertiseServices: adv}})
	return err
}

func (p *ProxyConfig) serve() error {
	return http.Serve(p.ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, err := p.lc.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, "<html><body><h1>Hello, tailnet!</h1>\n")
		fmt.Fprintf(w, "<p>You are <b>%s</b> from <b>%s</b> (%s)</p>", html.EscapeString(who.UserProfile.LoginName), r.RemoteAddr)
	}))
}

func main() {
	root := &cobra.Command{
		Use:   "tailscale-gateway-proxy",
		Short: "Tailscale Gateway Proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			v := viper.New()
			v.AutomaticEnv()
			v.SetDefault("addr", ":80")
			v.SetDefault("hostname", "tshello")
			v.SetDefault("ts_serve_config", "/etc/tailscaled/services.hujson")
			_ = v.BindEnv("addr", "ADDR")
			_ = v.BindEnv("hostname", "HOSTNAME")
			_ = v.BindEnv("ts_serve_config", "TS_SERVE_CONFIG")

			_ = v.BindPFlag("addr", cmd.Flags().Lookup("addr"))
			_ = v.BindPFlag("hostname", cmd.Flags().Lookup("hostname"))
			_ = v.BindPFlag("ts_serve_config", cmd.Flags().Lookup("config"))

			pc := &ProxyConfig{
				Addr:       v.GetString("addr"),
				Hostname:   v.GetString("hostname"),
				ConfigPath: v.GetString("ts_serve_config"),
			}
			if err := pc.start(); err != nil {
				return err
			}
			defer pc.s.Close()
			defer pc.ln.Close()

			cfg, err := tsconfig.ParseFile(pc.ConfigPath)
			if err != nil {
				return fmt.Errorf("failed to parse services config: %w", err)
			}
			if err := pc.advertise(cfg); err != nil {
				return fmt.Errorf("error setting prefs AdvertiseServices: %w", err)
			}
			return pc.serve()
		},
	}

	root.Flags().String("addr", ":80", "address to listen on")
	root.Flags().String("hostname", "tshello", "hostname to use on the tailnet")
	root.Flags().String("config", "/etc/tailscaled/services.hujson", "path to services.hujson")

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}
