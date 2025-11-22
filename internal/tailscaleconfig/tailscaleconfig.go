package tailscaleconfig

import (
	"fmt"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// Config represents a Tailscale serve configuration.
type Config struct {
	cfg *ipn.ServeConfig
}

// Marshal serializes the Config into HUJSON-formatted Tailscale serve config bytes.
func (c *Config) Marshal() ([]byte, error) { return Marshal(c) }

type serviceOptions struct {
	HTTPRoutes []*gatewayv1.HTTPRoute
	Host       string
}

// Option modifies serviceOptions used to build a Config.
type Option func(*serviceOptions)

// makeOptions applies a series of Option functions to serviceOptions.
func makeOptions(opts []Option) serviceOptions {
	o := serviceOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithHTTPRoute adds a single HTTPRoute to service options.
func WithHTTPRoute(hr *gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) { o.HTTPRoutes = append(o.HTTPRoutes, hr) }
}

// WithHTTPRoutes adds multiple HTTPRoutes to service options.
func WithHTTPRoutes(hrs []gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) {
		for i := range hrs {
			o.HTTPRoutes = append(o.HTTPRoutes, &hrs[i])
		}
	}
}

// WithHost sets the optional DNS suffix to append to hostnames.
func WithHost(name string) Option {
	return func(o *serviceOptions) { o.Host = name }
}

// NewConfig builds a Tailscale serve Config from a Gateway and HTTPRoutes.
func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := makeOptions(opts)
	if gw == nil {
		return nil, fmt.Errorf("gateway is nil")
	}

	cfg := newServeConfig()

	// Web handlers for hostnames or default gateway hostname
	for _, hr := range o.HTTPRoutes {
		// Determine service key: hostnames or gateway name
		var serviceKeys []tailcfg.ServiceName
		if len(hr.Spec.Hostnames) == 0 {
			serviceKeys = []tailcfg.ServiceName{
				tailcfg.AsServiceName(fmt.Sprintf("svc:%s", gw.Name)),
			}
		} else {
			for _, host := range hr.Spec.Hostnames {
				serviceKeys = append(serviceKeys, tailcfg.AsServiceName(fmt.Sprintf("svc:%s", host)))
			}
		}

		for _, svcName := range serviceKeys {
			if _, ok := cfg.cfg.Services[svcName]; !ok {
				cfg.cfg.Services[svcName] = &ipn.ServiceConfig{
					TCP: map[uint16]*ipn.TCPPortHandler{},
					Web: map[ipn.HostPort]*ipn.WebServerConfig{},
				}
			}
			for _, l := range gw.Spec.Listeners {
				switch l.Protocol {
				case gatewayv1.HTTPProtocolType:
					cfg.cfg.Services[svcName].TCP[uint16(l.Port)] = &ipn.TCPPortHandler{HTTP: true}
				case gatewayv1.HTTPSProtocolType:
					cfg.cfg.Services[svcName].TCP[uint16(l.Port)] = &ipn.TCPPortHandler{HTTPS: true}
				default:
					continue
				}
			}

			for _, pr := range hr.Spec.ParentRefs {
				if string(pr.Name) == string(gw.Name) {
					if pr.Namespace == nil || string(*pr.Namespace) == string(gw.Namespace) {
						for _, l := range gw.Spec.Listeners {
							if l.Protocol != gatewayv1.HTTPProtocolType &&
								l.Protocol != gatewayv1.HTTPSProtocolType {
								continue
							}
							if len(hr.Spec.Hostnames) == 0 {
								host := fmt.Sprintf("%s-%s", gw.Namespace, gw.Name)
								if o.Host != "" {
									host = fmt.Sprintf("%s.%s", host, o.Host)
								}
								addr := ipn.HostPort(fmt.Sprintf("%s:%d", host, l.Port))
								if _, ok := cfg.cfg.Services[svcName].Web[addr]; !ok {
									cfg.cfg.Services[svcName].Web[addr] = &ipn.WebServerConfig{
										Handlers: map[string]*ipn.HTTPHandler{},
									}
								}
								if cfg.cfg.Services[svcName].Web[addr].Handlers == nil {
									cfg.cfg.Services[svcName].Web[addr].Handlers = map[string]*ipn.HTTPHandler{}
								}
								for _, rule := range hr.Spec.Rules {
									upstreamPort := int(l.Port)
									if l.Protocol == gatewayv1.HTTPSProtocolType {
										upstreamPort = 80
									}
									upstream := fmt.Sprintf("http://127.0.0.1:%d", upstreamPort)
									if len(rule.Matches) == 0 {
										cfg.cfg.Services[svcName].Web[addr].Handlers["/"] = &ipn.HTTPHandler{
											Proxy: upstream,
										}
										continue
									}
									for _, match := range rule.Matches {
										if match.Path == nil {
											continue
										}
										mt := gatewayv1.PathMatchPathPrefix
										if match.Path.Type != nil {
											mt = *match.Path.Type
										}
										if mt != gatewayv1.PathMatchPathPrefix {
											continue
										}
										if match.Path.Value == nil {
											continue
										}
										path := *match.Path.Value
										if path == "" {
											continue
										}
                                        cfg.cfg.Services[svcName].Web[addr].Handlers[path] = &ipn.HTTPHandler{
                                            Proxy: upstream,
                                        }
									}
								}
							} else {
								for _, h := range hr.Spec.Hostnames {
									host := string(h)
									if o.Host != "" {
										host = fmt.Sprintf("%s.%s", host, o.Host)
									}
									addr := ipn.HostPort(fmt.Sprintf("%s:%d", host, l.Port))
									if _, ok := cfg.cfg.Services[svcName].Web[addr]; !ok {
										cfg.cfg.Services[svcName].Web[addr] = &ipn.WebServerConfig{Handlers: map[string]*ipn.HTTPHandler{}}
									}
									if cfg.cfg.Services[svcName].Web[addr].Handlers == nil {
										cfg.cfg.Services[svcName].Web[addr].Handlers = map[string]*ipn.HTTPHandler{}
									}
									for _, rule := range hr.Spec.Rules {
										upstreamPort := int(l.Port)
										if l.Protocol == gatewayv1.HTTPSProtocolType {
											upstreamPort = 80
										}
										upstream := fmt.Sprintf("http://127.0.0.1:%d", upstreamPort)
										if len(rule.Matches) == 0 {
											cfg.cfg.Services[svcName].Web[addr].Handlers["/"] = &ipn.HTTPHandler{Proxy: upstream}
											continue
										}
										for _, match := range rule.Matches {
											if match.Path == nil {
												continue
											}
											mt := gatewayv1.PathMatchPathPrefix
											if match.Path.Type != nil {
												mt = *match.Path.Type
											}
											if mt != gatewayv1.PathMatchPathPrefix {
												continue
											}
											if match.Path.Value == nil {
												continue
											}
											path := *match.Path.Value
											if path == "" {
												continue
											}
                                            cfg.cfg.Services[svcName].Web[addr].Handlers[path] = &ipn.HTTPHandler{Proxy: upstream}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return cfg, nil
}

// newServeConfig returns an empty Config with initialized internal maps.
func newServeConfig() *Config {
	return &Config{cfg: &ipn.ServeConfig{Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{}}}
}

// AdvertiseServicesCommand returns a shell command to advertise all services.
func AdvertiseServicesCommand(cfg *Config) ([]string, error) {
	var svcs []string
	for svcName := range cfg.cfg.Services {
		svcs = append(svcs, string(svcName))
	}
	cmds := make([]string, len(svcs))
	for i, svc := range svcs {
		cmds[i] = fmt.Sprintf("tailscale serve advertise %s", svc)
	}
	return []string{"/bin/sh", "-c", strings.Join(cmds, "\n")}, nil
}

// DrainServicesCommand returns a shell command to drain all services, ignoring errors.
func DrainServicesCommand(cfg *Config) ([]string, error) {
	var svcs []string
	for svcName := range cfg.cfg.Services {
		svcs = append(svcs, string(svcName))
	}
	cmds := make([]string, len(svcs))
	for i, svc := range svcs {
		cmds[i] = fmt.Sprintf("tailscale serve drain %s || true", svc)
	}
	return []string{"/bin/sh", "-c", strings.Join(cmds, "\n")}, nil
}
