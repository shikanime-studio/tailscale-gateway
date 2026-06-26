package config

import (
	"fmt"
	"net/url"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// Config represents a Tailscale serve configuration.
type Config struct {
	cfg *ipn.ServeConfig
}

// Marshal serializes the Config into HUJSON-formatted Tailscale serve config bytes.
func (c *Config) Marshal() ([]byte, error) {
	return Marshal(c)
}

type options struct {
	HTTPRoutes []*gatewayv1.HTTPRoute
	Ingresses  []*networkingv1.Ingress
}

// Option modifies options used to build a Config.
type Option func(*options)

// makeOptions applies a series of Option functions to options.
func makeOptions(opts []Option) options {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithHTTPRoutes adds multiple HTTPRoutes to service options.
func WithHTTPRoutes(hrs []*gatewayv1.HTTPRoute) Option {
	return func(o *options) {
		o.HTTPRoutes = hrs
	}
}

// NewConfig builds a Tailscale serve Config from a Gateway and HTTPRoutes.
func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := makeOptions(opts)

	cfg := &Config{cfg: &ipn.ServeConfig{Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{}}}
	for _, hr := range o.HTTPRoutes {
		var serviceNames []tailcfg.ServiceName
		if len(hr.Spec.Hostnames) == 0 {
			serviceNames = []tailcfg.ServiceName{
				tailcfg.AsServiceName(fmt.Sprintf("svc:%s", gw.Name)),
			}
		} else {
			for _, hn := range hr.Spec.Hostnames {
				serviceNames = append(serviceNames, newServiceName(hn))
			}
		}
		for _, svcName := range serviceNames {
			if _, ok := cfg.cfg.Services[svcName]; !ok {
				svc, err := newServiceConfig(gw, hr)
				if err != nil {
					return nil, err
				}
				cfg.cfg.Services[svcName] = svc
			}
		}
	}

	if err := buildIngressServices(cfg, o.Ingresses); err != nil {
		return nil, err
	}

	return cfg, nil
}

func newServiceName(host gatewayv1.Hostname) tailcfg.ServiceName {
	return tailcfg.AsServiceName(fmt.Sprintf("svc:%s", host))
}

// newServiceConfig builds a Tailscale service config from a Gateway and options.
func newServiceConfig(gw *gatewayv1.Gateway, hr *gatewayv1.HTTPRoute) (*ipn.ServiceConfig, error) {
	web, err := newWebServerConfigs(gw, hr)
	if err != nil {
		return nil, err
	}
	svc := &ipn.ServiceConfig{
		TCP: newTCPPortHandlers(gw),
		Web: web,
	}
	return svc, nil
}

// newTCPPortHandlers builds TCP port handlers for a Gateway.
func newTCPPortHandlers(gw *gatewayv1.Gateway) map[uint16]*ipn.TCPPortHandler {
	tcp := map[uint16]*ipn.TCPPortHandler{}
	for _, l := range gw.Spec.Listeners {
		switch l.Protocol {
		case gatewayv1.HTTPProtocolType:
			tcp[uint16(l.Port)] = &ipn.TCPPortHandler{HTTP: true}
		case gatewayv1.HTTPSProtocolType:
			tcp[uint16(l.Port)] = &ipn.TCPPortHandler{HTTPS: true}
		case gatewayv1.TLSProtocolType, gatewayv1.TCPProtocolType, gatewayv1.UDPProtocolType:
			continue
		}
	}
	return tcp
}

// newWebServerConfigs builds web server configs for a Gateway and service options.
func newWebServerConfigs(
	gw *gatewayv1.Gateway,
	hr *gatewayv1.HTTPRoute,
) (map[ipn.HostPort]*ipn.WebServerConfig, error) {
	web := map[ipn.HostPort]*ipn.WebServerConfig{}

	for _, pr := range hr.Spec.ParentRefs {
		if !isParentGateway(gw, &pr) {
			continue
		}

		for _, l := range gw.Spec.Listeners {
			if !isSupportedProtocol(l.Protocol) {
				return nil, fmt.Errorf("only HTTP and HTTPS protocols are supported")
			}
			var hosts []string
			if len(hr.Spec.Hostnames) == 0 {
				hosts = []string{gw.Name}
			} else {
				for _, h := range hr.Spec.Hostnames {
					hosts = append(hosts, string(h))
				}
			}
			for _, h := range hosts {
				addr := ipn.HostPort(fmt.Sprintf("%s:%d", h, l.Port))
				handlers, err := newHTTPHandlers(hr)
				if err != nil {
					return nil, err
				}
				web[addr] = &ipn.WebServerConfig{Handlers: handlers}
			}
		}
	}

	return web, nil
}

// isParentGateway returns true if the parent reference is the gateway.
func isParentGateway(gw *gatewayv1.Gateway, pr *gatewayv1.ParentReference) bool {
	gwNs := gatewayv1.Namespace(gw.Namespace)
	prNs := ptr.Deref(pr.Namespace, gwNs)
	return string(pr.Name) == gw.Name && (prNs == gwNs)
}

// isSupportedProtocol returns true if the protocol is supported.
func isSupportedProtocol(protocol gatewayv1.ProtocolType) bool {
	return protocol == gatewayv1.HTTPProtocolType || protocol == gatewayv1.HTTPSProtocolType
}

// newHTTPHandlers builds HTTP handlers for a HTTPRoute.
func newHTTPHandlers(hr *gatewayv1.HTTPRoute) (map[string]*ipn.HTTPHandler, error) {
	handlers := map[string]*ipn.HTTPHandler{}

	for _, rule := range hr.Spec.Rules {
		if len(rule.BackendRefs) > 1 {
			return nil, fmt.Errorf("multiple BackendRefs in a single rule are not supported")
		}

		if len(rule.Matches) == 0 {
			handler, err := newHTTPHandler(rule.BackendRefs[0], "/")
			if err != nil {
				return nil, err
			}
			handlers["/"] = handler
		} else {
			for _, match := range rule.Matches {
				if !isSupportedMatch(match) {
					continue
				}
				handler, err := newMatchHandler(rule.BackendRefs[0], match)
				if err != nil {
					return nil, err
				}
				handlers[*match.Path.Value] = handler
			}
		}
	}

	return handlers, nil
}

// isSupportedMatch returns true if the match is supported.
func isSupportedMatch(match gatewayv1.HTTPRouteMatch) bool {
	if match.Path == nil {
		return false
	}
	if match.Path.Value == nil {
		return false
	}
	if match.Path.Type == nil {
		return true
	}
	return *match.Path.Type == gatewayv1.PathMatchPathPrefix
}

// newMatchHandler builds an HTTP handler for a BackendRef and path match.
func newMatchHandler(
	br gatewayv1.HTTPBackendRef,
	match gatewayv1.HTTPRouteMatch,
) (*ipn.HTTPHandler, error) {
	if match.Path == nil {
		return nil, fmt.Errorf("path match is required")
	}
	if match.Path.Value == nil {
		return nil, fmt.Errorf("path match value is required")
	}
	return newHTTPHandler(br, *match.Path.Value)
}

// newHTTPHandler builds an HTTP handler for a BackendRef and path match.
func newHTTPHandler(br gatewayv1.HTTPBackendRef, path string) (*ipn.HTTPHandler, error) {
	host := string(br.Name)
	if br.Namespace != nil {
		host = fmt.Sprintf("%s.%s", host, *br.Namespace)
	}
	if br.Port != nil {
		host = fmt.Sprintf("%s:%d", host, *br.Port)
	}
	proxy := url.URL{
		Scheme: "http",
		Host:   host,
		Path:   path,
	}
	return &ipn.HTTPHandler{Proxy: proxy.String()}, nil
}

// AdvertiseServicesCommand returns a shell command to advertise all services.
func AdvertiseServicesCommand(cfg *Config) ([]string, error) {
	svcs := make([]string, 0, len(cfg.cfg.Services))
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
	svcs := make([]string, 0, len(cfg.cfg.Services))
	for svcName := range cfg.cfg.Services {
		svcs = append(svcs, string(svcName))
	}
	cmds := make([]string, len(svcs))
	for i, svc := range svcs {
		cmds[i] = fmt.Sprintf("tailscale serve drain %s || true", svc)
	}
	return []string{"/bin/sh", "-c", strings.Join(cmds, "\n")}, nil
}
