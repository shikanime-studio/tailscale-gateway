package services

import (
	"encoding/json"
	"fmt"

	"github.com/tailscale/hujson"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	conffile "tailscale.com/ipn/conffile"
	"tailscale.com/tailcfg"
)

// ServiceName returns the canonical Tailscale service name for a gateway.
func ServiceName(name string) tailcfg.ServiceName {
	return tailcfg.AsServiceName(fmt.Sprintf("svc:%s", name))
}

// NewServiceConfig creates an empty ServicesConfigFile with required fields.
func NewServiceConfig() *conffile.ServicesConfigFile {
	return &conffile.ServicesConfigFile{
		Version:  "0.0.1",
		Services: map[tailcfg.ServiceName]*conffile.ServiceDetailsFile{},
	}
}

// NewServiceDetails constructs empty ServiceDetails with endpoint map.
func NewServiceDetails() *conffile.ServiceDetailsFile {
	return &conffile.ServiceDetailsFile{
		Version:   "0.0.1",
		Endpoints: map[*tailcfg.ProtoPortRange]*conffile.Target{},
	}
}

type serviceOptions struct {
	Gateway    *gatewayv1.Gateway
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

// WithGateway includes a Gateway when computing services config.
func WithGateway(gw *gatewayv1.Gateway) Option {
	return func(o *serviceOptions) { o.Gateway = gw }
}

// WithHTTPRoute includes an HTTPRoute when computing services config.
func WithHTTPRoute(hr *gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) { o.HTTPRoutes = append(o.HTTPRoutes, hr) }
}

func WithHTTPRoutes(hrs []gatewayv1.HTTPRoute) Option {
	return func(o *serviceOptions) {
		for i := range hrs {
			hr := hrs[i]
			o.HTTPRoutes = append(o.HTTPRoutes, &hr)
		}
	}
}

// Apply populates the ServicesConfigFile using provided Gateways and Routes.
func Apply(cfg *conffile.ServicesConfigFile, opts ...Option) error {
	o := makeOptions(opts)

	gw := o.Gateway
	if gw == nil {
		return fmt.Errorf("gateway is required")
	}
	if cfg.Services == nil {
		cfg.Services = map[tailcfg.ServiceName]*conffile.ServiceDetailsFile{}
	}

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
				var proto string
				switch l.Protocol {
				case gatewayv1.HTTPProtocolType,
					gatewayv1.HTTPSProtocolType,
					gatewayv1.TLSProtocolType,
					gatewayv1.TCPProtocolType:
					proto = "tcp"
				case gatewayv1.UDPProtocolType:
					proto = "udp"
				default:
					proto = "tcp"
				}
				protoPortRange := &tailcfg.ProtoPortRange{}
				if err := protoPortRange.UnmarshalText([]byte(fmt.Sprintf("%s:%d", proto, l.Port))); err != nil {
					return err
				}

				// BackendRefs
				for _, r := range hr.Spec.Rules {
					for _, br := range r.BackendRefs {
						if br.Port == nil {
							continue
						}
						ns := hr.Namespace
						if br.Namespace != nil {
							ns = string(*br.Namespace)
						}
						targetURI := fmt.Sprintf(
							"http://%s.%s.svc.cluster.local:%d",
							br.Name,
							ns,
							*br.Port,
						)
						target := &conffile.Target{}
						if err := target.UnmarshalJSON([]byte(fmt.Sprintf("%q", targetURI))); err != nil {
							return err
						}
						if len(hr.Spec.Hostnames) == 0 {
							svcName := ServiceName(string(gw.Name))
							if _, ok := cfg.Services[svcName]; !ok {
								cfg.Services[svcName] = NewServiceDetails()
							}
							if _, exists := cfg.Services[svcName].Endpoints[protoPortRange]; !exists {
								cfg.Services[svcName].Endpoints[protoPortRange] = target
							}
						} else {
							for _, host := range hr.Spec.Hostnames {
								if l.Hostname != nil {
									if string(*l.Hostname) != string(host) {
										continue
									}
								}

								svcName := ServiceName(string(host))
								if _, ok := cfg.Services[svcName]; !ok {
									cfg.Services[svcName] = NewServiceDetails()
								}
								if _, exists := cfg.Services[svcName].Endpoints[protoPortRange]; !exists {
									cfg.Services[svcName].Endpoints[protoPortRange] = target
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// Unmarshal parses a services config JSON/HUJSON into a typed config.
func Unmarshal(b []byte) (*conffile.ServicesConfigFile, error) {
	cfg := &conffile.ServicesConfigFile{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Marshal encodes a services config as formatted HUJSON bytes.
func Marshal(cfg *conffile.ServicesConfigFile) ([]byte, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	fb, err := hujson.Format(b)
	if err != nil {
		return nil, err
	}
	return fb, nil
}
