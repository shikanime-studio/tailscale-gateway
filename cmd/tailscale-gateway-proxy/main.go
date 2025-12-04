package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/tsconfig"
	"github.com/spf13/cobra"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

var root = &cobra.Command{
	Use:   "tailscale-gateway-proxy",
	Short: "Tailscale Gateway Proxy",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.NewProxyConfig()
		if err != nil {
			return err
		}
		addr := cfg.GetProxyAddr()
		hostname := cfg.GetProxyHostname()
		servePath := cfg.GetTailscaleServeConfigPath()

		s := tsnet.Server{
			Hostname: hostname,
			AuthKey:  cfg.GetTailscaleAuthKey(),
			Dir:      cfg.GetTailscaleDir(),
		}
		ln, err := s.Listen("tcp", addr)
		if err != nil {
			return err
		}
		lc, err := s.LocalClient()
		if err != nil {
			return err
		}
		defer s.Close()
		defer ln.Close()

		tsCfg, err := tsconfig.ParseFile(servePath)
		if err != nil {
			return fmt.Errorf("failed to parse services config: %w", err)
		}
		var adv []string
		for _, n := range tsCfg.AdvertisedServices() {
			adv = append(adv, string(n))
		}
		if _, err := lc.EditPrefs(
			context.Background(),
			&ipn.MaskedPrefs{AdvertiseServicesSet: true, Prefs: ipn.Prefs{AdvertiseServices: adv}},
		); err != nil {
			return fmt.Errorf("error setting prefs AdvertiseServices: %w", err)
		}

		return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			fmt.Fprintf(w, "<html><body><h1>Hello, tailnet!</h1>\n")
			fmt.Fprintf(
				w,
				"<p>You are <b>%s</b> from <b>%s</b></p>",
				html.EscapeString(who.UserProfile.LoginName),
				r.RemoteAddr,
			)
		}))
	},
}

func init() {
	root.Flags().String("addr", ":80", "address to listen on")
	root.Flags().String("hostname", "tshello", "hostname to use on the tailnet")
	root.Flags().String("config", "/etc/tailscaled/services.hujson", "path to services.hujson")
}

func main() {
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}
