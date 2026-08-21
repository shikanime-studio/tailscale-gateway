package config

import (
	"fmt"
	"net/url"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
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
	TCPRoutes  []*gatewayv1alpha2.TCPRoute
	UDPRoutes  []*gatewayv1alpha2.UDPRoute
}

// Option modifies options used to build a Config.
type Option func(*options)

// buildOptions applies a series of Option functions to options.
func buildOptions(opts []Option) options {
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

// WithTCPRoutes adds multiple TCPRoutes to service options.
func WithTCPRoutes(trs []*gatewayv1alpha2.TCPRoute) Option {
	return func(o *options) {
		o.TCPRoutes = trs
	}
}

// WithUDPRoutes adds multiple UDPRoutes to service options.
func WithUDPRoutes(urs []*gatewayv1alpha2.UDPRoute) Option {
	return func(o *options) {
		o.UDPRoutes = urs
	}
}

// NewConfig builds a Tailscale serve Config from a Gateway and HTTPRoutes.
func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := buildOptions(opts)

	cfg := &Config{cfg: &ipn.ServeConfig{Services: map[tailcfg.ServiceName]*ipn.ServiceConfig{}}}
	for _, hr := range o.HTTPRoutes {
		var serviceNames []tailcfg.ServiceName
		if len(hr.Spec.Hostnames) == 0 {
			serviceNames = []tailcfg.ServiceName{
				tailcfg.AsServiceName(fmt.Sprintf("svc:%s", gw.Name)),
			}
		} else {
			for _, hn := range hr.Spec.Hostnames {
				serviceNames = append(serviceNames, buildServiceName(hn))
			}
		}
		for _, svcName := range serviceNames {
			if _, ok := cfg.cfg.Services[svcName]; !ok {
				svc, err := buildHTTPServiceConfig(gw, hr)
				if err != nil {
					return nil, err
				}
				cfg.cfg.Services[svcName] = svc
			}
		}
	}
	for _, tr := range o.TCPRoutes {
		svcName := tailcfg.AsServiceName(fmt.Sprintf("svc:%s", tr.Name))
		if _, ok := cfg.cfg.Services[svcName]; ok {
			continue
		}
		svc, err := buildTCPServiceConfig(gw, tr)
		if err != nil {
			return nil, err
		}
		cfg.cfg.Services[svcName] = svc
	}
	for _, ur := range o.UDPRoutes {
		svcName := tailcfg.AsServiceName(fmt.Sprintf("svc:%s", ur.Name))
		if _, ok := cfg.cfg.Services[svcName]; ok {
			continue
		}
		svc, err := buildUDPServiceConfig(gw, ur)
		if err != nil {
			return nil, err
		}
		cfg.cfg.Services[svcName] = svc
	}

	return cfg, nil
}

func buildServiceName(host gatewayv1.Hostname) tailcfg.ServiceName {
	return tailcfg.AsServiceName(fmt.Sprintf("svc:%s", host))
}

// buildHTTPServiceConfig builds a Tailscale service config from a Gateway and options.
func buildHTTPServiceConfig(
	gw *gatewayv1.Gateway,
	hr *gatewayv1.HTTPRoute,
) (*ipn.ServiceConfig, error) {
	web, err := buildWebServerConfigs(gw, hr)
	if err != nil {
		return nil, err
	}
	svc := &ipn.ServiceConfig{
		TCP: buildTCPPortHandlers(gw),
		Web: web,
	}
	return svc, nil
}

// buildTCPServiceConfig builds a Tailscale service config from a Gateway and TCPRoute.
func buildTCPServiceConfig(
	gw *gatewayv1.Gateway,
	tr *gatewayv1alpha2.TCPRoute,
) (*ipn.ServiceConfig, error) {
	port, err := buildListenerPortFromParentRefs(gw, tr.Spec.ParentRefs)
	if err != nil {
		return nil, err
	}
	forward, err := buildTCPForwardFromRoute(tr)
	if err != nil {
		return nil, err
	}
	return &ipn.ServiceConfig{
		TCP: map[uint16]*ipn.TCPPortHandler{
			port: {
				TCPForward: forward,
			},
		},
	}, nil
}

// buildUDPServiceConfig builds a Tailscale service config from a Gateway and UDPRoute.
func buildUDPServiceConfig(
	gw *gatewayv1.Gateway,
	ur *gatewayv1alpha2.UDPRoute,
) (*ipn.ServiceConfig, error) {
	_, err := buildListenerPortFromParentRefs(gw, ur.Spec.ParentRefs)
	if err != nil {
		return nil, err
	}
	return &ipn.ServiceConfig{Tun: true}, nil
}

func buildListenerPortFromParentRefs(
	gw *gatewayv1.Gateway,
	parentRefs []gatewayv1.ParentReference,
) (uint16, error) {
	listeners := map[gatewayv1.SectionName]gatewayv1.PortNumber{}
	for _, l := range gw.Spec.Listeners {
		listenerName := l.Name
		if listenerName == "" {
			continue
		}
		listeners[listenerName] = l.Port
	}
	for _, pr := range parentRefs {
		if pr.SectionName == nil {
			continue
		}
		if port, ok := listeners[*pr.SectionName]; ok {
			return uint16(port), nil
		}
	}
	if len(gw.Spec.Listeners) > 0 {
		return uint16(gw.Spec.Listeners[0].Port), nil
	}
	return 0, fmt.Errorf("no listener found for route")
}

func buildTCPForwardFromRoute(tr *gatewayv1alpha2.TCPRoute) (string, error) {
	if len(tr.Spec.Rules) == 0 {
		return "", fmt.Errorf("tcp route requires at least one rule")
	}
	if len(tr.Spec.Rules[0].BackendRefs) == 0 {
		return "", fmt.Errorf("tcp route requires at least one backend ref")
	}
	br := tr.Spec.Rules[0].BackendRefs[0]
	host := string(br.Name)
	if br.Namespace != nil {
		host = fmt.Sprintf("%s.%s", host, *br.Namespace)
	}
	if br.Port != nil {
		host = fmt.Sprintf("%s:%d", host, *br.Port)
	}
	return host, nil
}

// buildTCPPortHandlers builds TCP port handlers for a Gateway.
func buildTCPPortHandlers(gw *gatewayv1.Gateway) map[uint16]*ipn.TCPPortHandler {
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

// buildWebServerConfigs builds web server configs for a Gateway and service options.
func buildWebServerConfigs(
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
				continue
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
				handlers, err := buildHTTPHandlers(hr)
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

// buildHTTPHandlers builds HTTP handlers for a HTTPRoute.
func buildHTTPHandlers(hr *gatewayv1.HTTPRoute) (map[string]*ipn.HTTPHandler, error) {
	handlers := map[string]*ipn.HTTPHandler{}

	for _, rule := range hr.Spec.Rules {
		if len(rule.BackendRefs) > 1 {
			return nil, fmt.Errorf("multiple BackendRefs in a single rule are not supported")
		}

		if len(rule.Matches) == 0 {
			handler, err := buildHTTPHandler(rule.BackendRefs[0], "/")
			if err != nil {
				return nil, err
			}
			handlers["/"] = handler
		} else {
			for _, match := range rule.Matches {
				if !isSupportedMatch(match) {
					continue
				}
				handler, err := buildMatchHandler(rule.BackendRefs[0], match)
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

// buildMatchHandler builds an HTTP handler for a BackendRef and path match.
func buildMatchHandler(
	br gatewayv1.HTTPBackendRef,
	match gatewayv1.HTTPRouteMatch,
) (*ipn.HTTPHandler, error) {
	if match.Path == nil {
		return nil, fmt.Errorf("path match is required")
	}
	if match.Path.Value == nil {
		return nil, fmt.Errorf("path match value is required")
	}
	return buildHTTPHandler(br, *match.Path.Value)
}

// buildHTTPHandler builds an HTTP handler for a BackendRef and path match.
func buildHTTPHandler(br gatewayv1.HTTPBackendRef, path string) (*ipn.HTTPHandler, error) {
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
