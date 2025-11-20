package tailscaleconfig

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	conffile "tailscale.com/ipn/conffile"
	"tailscale.com/tailcfg"
	"tailscale.com/types/ipproto"
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

// Option mutates serviceOptions to include resources relevant to config build.
type Option func(*serviceOptions)

// makeOptions applies a list of Option to produce a concrete serviceOptions.
func makeOptions(opts []Option) serviceOptions {
	o := serviceOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithHTTPRoute includes an HTTPRoute when computing services config.
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

// NewConfig builds a services.Config for a Gateway with options.
func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := makeOptions(opts)

	if gw == nil {
		return nil, fmt.Errorf("gateway is nil")
	}

	cfg := newService()
	for _, hr := range o.HTTPRoutes {
		// ParentRefs
		for _, pr := range hr.Spec.ParentRefs {
			if string(pr.Name) != string(gw.Name) {
				continue
			}
			if pr.Namespace != nil && string(*pr.Namespace) != string(gw.Namespace) {
				continue
			}

			// Listeners
			for _, l := range gw.Spec.Listeners {
				protoPortRange, err := protoPortRangeFromListener(&l)
				if err != nil {
					return nil, fmt.Errorf("failed to parse proto port range: %w", err)
				}

				// BackendRefs
				for _, r := range hr.Spec.Rules {
					for _, br := range r.BackendRefs {
						if br.Port == nil {
							return nil, fmt.Errorf("backend port is nil")
						}
						target, err := targetFromBackendRef(&br.BackendRef)
						if err != nil {
							return nil, fmt.Errorf("failed to build target: %w", err)
						}
						if len(hr.Spec.Hostnames) == 0 {
							svcName := serviceName(string(gw.Name))
							if _, ok := cfg.cfg.Services[svcName]; !ok {
								cfg.cfg.Services[svcName] = newServiceDetails()
							}
							if _, exists := cfg.cfg.Services[svcName].Endpoints[protoPortRange]; !exists {
								cfg.cfg.Services[svcName].Endpoints[protoPortRange] = target
							}
						} else {
							for _, host := range hr.Spec.Hostnames {
								if l.Hostname != nil {
									if string(*l.Hostname) != string(host) {
										continue
									}
								}

								svcName := serviceName(string(host))
								if _, ok := cfg.cfg.Services[svcName]; !ok {
									cfg.cfg.Services[svcName] = newServiceDetails()
								}
								if _, exists := cfg.cfg.Services[svcName].Endpoints[protoPortRange]; !exists {
									cfg.cfg.Services[svcName].Endpoints[protoPortRange] = target
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

// serviceName returns the canonical Tailscale service name for a gateway.
func serviceName(name string) tailcfg.ServiceName {
	return tailcfg.AsServiceName(fmt.Sprintf("svc:%s", name))
}

// newService constructs empty services config.
func newService() *Config {
	return &Config{cfg: &conffile.ServicesConfigFile{
		Version:  "0.0.1",
		Services: map[tailcfg.ServiceName]*conffile.ServiceDetailsFile{},
	}}
}

// newServiceDetails constructs empty newServiceDetails with endpoint map.
func newServiceDetails() *conffile.ServiceDetailsFile {
	return &conffile.ServiceDetailsFile{
		Endpoints: map[*tailcfg.ProtoPortRange]*conffile.Target{},
	}
}

func protoPortRangeFromListener(
	l *gatewayv1.Listener,
) (*tailcfg.ProtoPortRange, error) {
	proto, err := protoFromListener(l)
	if err != nil {
		return nil, err
	}
	protoPortRange := &tailcfg.ProtoPortRange{}
	if err := protoPortRange.UnmarshalText([]byte(fmt.Sprintf("%s:%d", proto, l.Port))); err != nil {
		return nil, err
	}
	return protoPortRange, nil
}

func protoFromListener(l *gatewayv1.Listener) (string, error) {
	var proto ipproto.Proto
	switch l.Protocol {
	case gatewayv1.HTTPProtocolType,
		gatewayv1.HTTPSProtocolType,
		gatewayv1.TLSProtocolType,
		gatewayv1.TCPProtocolType:
		proto = ipproto.TCP
	case gatewayv1.UDPProtocolType:
		proto = ipproto.UDP
	default:
		proto = ipproto.TCP
	}
	text, err := ipproto.Proto(proto).MarshalText()
	if err != nil {
		return "", err
	}
	return string(text), nil
}

func targetFromBackendRef(br *gatewayv1.BackendRef) (*conffile.Target, error) {
	proto, err := serviceProtocolFromBackendRef(br)
	if err != nil {
		return nil, err
	}
	return &conffile.Target{
		Protocol:    proto,
		Destination: "127.0.0.1",
		DestinationPorts: tailcfg.PortRange{
			First: uint16(*br.Port),
			Last:  uint16(*br.Port),
		},
	}, nil
}

func serviceProtocolFromBackendRef(br *gatewayv1.BackendRef) (conffile.ServiceProtocol, error) {
	switch *br.Port {
	case 80:
		return conffile.ProtoHTTP, nil
	case 443:
		return conffile.ProtoHTTPS, nil
	default:
		return "", fmt.Errorf("unsupported port: %d", *br.Port)
	}
}
