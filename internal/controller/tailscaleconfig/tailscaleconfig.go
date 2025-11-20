package tailscaleconfig

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	conffile "tailscale.com/ipn/conffile"
	"tailscale.com/tailcfg"
	"tailscale.com/types/opt"
)

type Config struct {
	cfg *conffile.ServicesConfigFile
}

func (c *Config) Marshal() ([]byte, error) {
	return Marshal(c)
}

type serviceOptions struct {
	HTTPRoutes []*gatewayv1.HTTPRoute
}

type Option func(*serviceOptions)

func makeOptions(opts []Option) serviceOptions {
	o := serviceOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func WithHTTPRoute(hr *gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) { o.HTTPRoutes = append(o.HTTPRoutes, hr) }
}
func WithHTTPRoutes(hrs []gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) {
		for i := range hrs {
			o.HTTPRoutes = append(o.HTTPRoutes, &hrs[i])
		}
	}
}

func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := makeOptions(opts)
	if gw == nil {
		return nil, fmt.Errorf("gateway is nil")
	}

	cfg := newServices()
	for _, hr := range o.HTTPRoutes {
		for _, pr := range hr.Spec.ParentRefs {
			if string(pr.Name) != string(gw.Name) {
				continue
			}
			if pr.Namespace != nil && string(*pr.Namespace) != string(gw.Namespace) {
				continue
			}

			for _, l := range gw.Spec.Listeners {
				// Build source key proto:port (tcp or udp)
				proto := "tcp"
				if l.Protocol == gatewayv1.UDPProtocolType {
					proto = "udp"
				}
				ppr := &tailcfg.ProtoPortRange{}
				if err := ppr.UnmarshalText([]byte(fmt.Sprintf("%s:%d", proto, l.Port))); err != nil {
					return nil, err
				}

				// Target: http(s)://127.0.0.1:<listener port>
				tproto := conffile.ProtoHTTP
				if l.Port == 443 {
					tproto = conffile.ProtoHTTPS
				}
				target := &conffile.Target{
					Protocol:    tproto,
					Destination: "127.0.0.1",
					DestinationPorts: tailcfg.PortRange{
						First: uint16(l.Port),
						Last:  uint16(l.Port),
					},
				}

				if len(hr.Spec.Hostnames) == 0 {
					svc := serviceName(string(gw.Name))
					if _, ok := cfg.cfg.Services[svc]; !ok {
						cfg.cfg.Services[svc] = newServiceDetails()
					}
					if _, exists := cfg.cfg.Services[svc].Endpoints[ppr]; !exists {
						cfg.cfg.Services[svc].Endpoints[ppr] = target
					}
				} else {
					for _, h := range hr.Spec.Hostnames {
						svc := serviceName(string(h))
						if _, ok := cfg.cfg.Services[svc]; !ok {
							cfg.cfg.Services[svc] = newServiceDetails()
						}
						if _, exists := cfg.cfg.Services[svc].Endpoints[ppr]; !exists {
							cfg.cfg.Services[svc].Endpoints[ppr] = target
						}
					}
				}
			}
		}
	}
	return cfg, nil
}

func serviceName(name string) tailcfg.ServiceName {
	return tailcfg.AsServiceName(fmt.Sprintf("svc:%s", name))
}

func newServices() *Config {
	return &Config{
		cfg: &conffile.ServicesConfigFile{
			Version:  "0.0.1",
			Services: map[tailcfg.ServiceName]*conffile.ServiceDetailsFile{},
		},
	}
}

func newServiceDetails() *conffile.ServiceDetailsFile {
	return &conffile.ServiceDetailsFile{
		Endpoints:  map[*tailcfg.ProtoPortRange]*conffile.Target{},
		Advertised: opt.True,
	}
}

func ServiceNames(c *Config) []string {
	var names []string
	for n := range c.cfg.Services {
		names = append(names, n.String())
	}
	sort.Strings(names)
	return names
}

var AdvertiseServicesScript = template.Must(template.New("postStart").Parse(`
until tailscale status >/dev/null 2>&1; do sleep 1; done
{{- range .Services }}
tailscale serve advertise {{ . }}
{{- end }}
`))

var DrainServicesScript = template.Must(template.New("preStop").Parse(`
{{- range .Services }}
tailscale serve drain {{ . }} || true
{{- end }}
`))

func PostStartSetConfigCommand(services []string) []string {
	var buf bytes.Buffer
	_ = AdvertiseServicesScript.Execute(&buf, struct{ Services []string }{Services: services})
	return []string{"/bin/sh", "-c", buf.String()}
}

func PreStopDrainCommand(services []string) []string {
	var buf bytes.Buffer
	_ = DrainServicesScript.Execute(&buf, struct{ Services []string }{Services: services})
	return []string{"/bin/sh", "-c", buf.String()}
}
