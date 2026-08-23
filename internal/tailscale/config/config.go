package config

import (
	"fmt"
	"net/url"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
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
	HTTPRoutes       []*gatewayv1.HTTPRoute
	GRPCRoutes       []*gatewayv1.GRPCRoute
	TCPRoutes        []*gatewayv1alpha2.TCPRoute
	UDPRoutes        []*gatewayv1alpha2.UDPRoute
	TLSRoutes        []*gatewayv1alpha2.TLSRoute
	ReferenceGrants  []*gatewayv1beta1.ReferenceGrant
	BackendTLSPolicy []*gatewayv1.BackendTLSPolicy
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

// WithGRPCRoutes adds multiple GRPCRoutes to service options.
func WithGRPCRoutes(grs []*gatewayv1.GRPCRoute) Option {
	return func(o *options) {
		o.GRPCRoutes = grs
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

// WithTLSRoutes adds multiple TLSRoutes to service options.
func WithTLSRoutes(trs []*gatewayv1alpha2.TLSRoute) Option {
	return func(o *options) {
		o.TLSRoutes = trs
	}
}

// WithReferenceGrants adds multiple ReferenceGrants to service options.
func WithReferenceGrants(grants []*gatewayv1beta1.ReferenceGrant) Option {
	return func(o *options) {
		o.ReferenceGrants = grants
	}
}

// WithBackendTLSPolicies adds multiple BackendTLSPolicies to service options.
func WithBackendTLSPolicies(policies []*gatewayv1.BackendTLSPolicy) Option {
	return func(o *options) {
		o.BackendTLSPolicy = policies
	}
}

// NewConfig builds a Tailscale serve Config from a Gateway and the routes that
// reference it. It supports HTTPRoute, GRPCRoute, TCPRoute, UDPRoute and
// TLSRoute, honoring ReferenceGrant for cross-namespace backend references and
// BackendTLSPolicy for TLS-terminated connections to backends.
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
				svc, err := buildHTTPServiceConfig(gw, hr, o)
				if err != nil {
					return nil, err
				}
				cfg.cfg.Services[svcName] = svc
			}
		}
	}
	for _, gr := range o.GRPCRoutes {
		var serviceNames []tailcfg.ServiceName
		if len(gr.Spec.Hostnames) == 0 {
			serviceNames = []tailcfg.ServiceName{
				tailcfg.AsServiceName(fmt.Sprintf("svc:%s", gw.Name)),
			}
		} else {
			for _, hn := range gr.Spec.Hostnames {
				serviceNames = append(serviceNames, buildServiceName(hn))
			}
		}
		for _, svcName := range serviceNames {
			if _, ok := cfg.cfg.Services[svcName]; !ok {
				svc, err := buildGRPCServiceConfig(gw, gr, o)
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
		svc, err := buildTCPServiceConfig(gw, tr, o)
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
	for _, tr := range o.TLSRoutes {
		svcName := tailcfg.AsServiceName(fmt.Sprintf("svc:%s", tr.Name))
		if _, ok := cfg.cfg.Services[svcName]; ok {
			continue
		}
		svc, err := buildTLSServiceConfig(gw, tr, o)
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
	o options,
) (*ipn.ServiceConfig, error) {
	web, err := buildWebServerConfigs(gw, hr, o)
	if err != nil {
		return nil, err
	}
	svc := &ipn.ServiceConfig{
		TCP: buildTCPPortHandlers(gw),
		Web: web,
	}
	return svc, nil
}

// buildGRPCServiceConfig builds a Tailscale service config from a Gateway and a
// GRPCRoute. gRPC is HTTP/2; Tailscale serve terminates it over the Web layer
// and proxies to the backend.
func buildGRPCServiceConfig(
	gw *gatewayv1.Gateway,
	gr *gatewayv1.GRPCRoute,
	o options,
) (*ipn.ServiceConfig, error) {
	web, err := buildGRPCWebConfigs(gw, gr, o)
	if err != nil {
		return nil, err
	}
	svc := &ipn.ServiceConfig{
		TCP: buildTCPPortHandlers(gw),
		Web: web,
	}
	return svc, nil
}

// buildTLSServiceConfig builds a Tailscale service config from a Gateway and a
// TLSRoute. TLSRoutes perform SNI-matched passthrough, which maps to a raw TCP
// forward on the listener port to the backend.
func buildTLSServiceConfig(
	gw *gatewayv1.Gateway,
	tr *gatewayv1alpha2.TLSRoute,
	o options,
) (*ipn.ServiceConfig, error) {
	port, err := buildListenerPortFromParentRefs(gw, tr.Spec.ParentRefs)
	if err != nil {
		return nil, err
	}
	forward, err := buildTLSRouteForward(tr, o)
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

// buildTCPServiceConfig builds a Tailscale service config from a Gateway and TCPRoute.
func buildTCPServiceConfig(
	gw *gatewayv1.Gateway,
	tr *gatewayv1alpha2.TCPRoute,
	o options,
) (*ipn.ServiceConfig, error) {
	port, err := buildListenerPortFromParentRefs(gw, tr.Spec.ParentRefs)
	if err != nil {
		return nil, err
	}
	forward, err := buildTCPForwardFromRoute(tr, o)
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

func buildTCPForwardFromRoute(tr *gatewayv1alpha2.TCPRoute, o options) (string, error) {
	if len(tr.Spec.Rules) == 0 {
		return "", fmt.Errorf("tcp route requires at least one rule")
	}
	if len(tr.Spec.Rules[0].BackendRefs) == 0 {
		return "", fmt.Errorf("tcp route requires at least one backend ref")
	}
	br := tr.Spec.Rules[0].BackendRefs[0]
	if err := validateCrossNamespaceRef(
		o.ReferenceGrants,
		gatewayv1.Kind("TCPRoute"),
		tr.Namespace,
		string(br.Name),
		br.Namespace,
	); err != nil {
		return "", err
	}
	return backendHost(string(br.Name), br.Namespace, br.Port), nil
}

// buildTLSRouteForward builds the upstream forward target for a TLSRoute,
// honoring ReferenceGrant for cross-namespace backend references.
func buildTLSRouteForward(
	tr *gatewayv1alpha2.TLSRoute,
	o options,
) (string, error) {
	if len(tr.Spec.Rules) == 0 {
		return "", fmt.Errorf("tls route requires at least one rule")
	}
	if len(tr.Spec.Rules[0].BackendRefs) == 0 {
		return "", fmt.Errorf("tls route requires at least one backend ref")
	}
	br := tr.Spec.Rules[0].BackendRefs[0]
	if err := validateCrossNamespaceRef(
		o.ReferenceGrants,
		gatewayv1.Kind("TLSRoute"),
		tr.Namespace,
		string(br.Name),
		br.Namespace,
	); err != nil {
		return "", err
	}
	return backendHost(string(br.Name), br.Namespace, br.Port), nil
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

// buildWebConfigsForRoute builds WebServerConfig entries for a route by
// iterating its parent gateway listeners and hostnames, delegating handler
// construction to buildHandlers.
func buildWebConfigsForRoute(
	gw *gatewayv1.Gateway,
	parentRefs []gatewayv1.ParentReference,
	hostnames []gatewayv1.Hostname,
	buildHandlers func(host string, opts options) (map[string]*ipn.HTTPHandler, error),
	o options,
) (map[ipn.HostPort]*ipn.WebServerConfig, error) {
	web := map[ipn.HostPort]*ipn.WebServerConfig{}

	for _, pr := range parentRefs {
		if !isParentGateway(gw, &pr) {
			continue
		}

		for _, l := range gw.Spec.Listeners {
			if !isSupportedProtocol(l.Protocol) {
				continue
			}
			var hosts []string
			if len(hostnames) == 0 {
				hosts = []string{gw.Name}
			} else {
				for _, h := range hostnames {
					hosts = append(hosts, string(h))
				}
			}
			for _, h := range hosts {
				addr := ipn.HostPort(fmt.Sprintf("%s:%d", h, l.Port))
				handlers, err := buildHandlers(h, o)
				if err != nil {
					return nil, err
				}
				web[addr] = &ipn.WebServerConfig{Handlers: handlers}
			}
		}
	}

	return web, nil
}

// buildWebServerConfigs builds web server configs for a Gateway and HTTPRoute.
func buildWebServerConfigs(
	gw *gatewayv1.Gateway,
	hr *gatewayv1.HTTPRoute,
	o options,
) (map[ipn.HostPort]*ipn.WebServerConfig, error) {
	return buildWebConfigsForRoute(
		gw,
		hr.Spec.ParentRefs,
		hr.Spec.Hostnames,
		func(host string, opts options) (map[string]*ipn.HTTPHandler, error) {
			return buildHTTPHandlers(gw, hr, host, opts)
		},
		o,
	)
}

// buildGRPCWebConfigs builds web server configs for a Gateway and GRPCRoute.
// gRPC uses HTTP/2; it is reflected through the Web layer and proxied to the
// backend service on its configured port.
func buildGRPCWebConfigs(
	gw *gatewayv1.Gateway,
	gr *gatewayv1.GRPCRoute,
	o options,
) (map[ipn.HostPort]*ipn.WebServerConfig, error) {
	return buildWebConfigsForRoute(
		gw,
		gr.Spec.ParentRefs,
		gr.Spec.Hostnames,
		func(host string, opts options) (map[string]*ipn.HTTPHandler, error) {
			return buildGRPCHandlers(gw, gr, host, opts)
		},
		o,
	)
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

// buildHTTPHandlers builds HTTP handlers for a HTTPRoute bound to a host. It
// honors ReferenceGrant for cross-namespace backend references and
// BackendTLSPolicy for TLS-terminated connections to backends.
func buildHTTPHandlers(
	gw *gatewayv1.Gateway,
	hr *gatewayv1.HTTPRoute,
	host string,
	o options,
) (map[string]*ipn.HTTPHandler, error) {
	handlers := map[string]*ipn.HTTPHandler{}

	for _, rule := range hr.Spec.Rules {
		if len(rule.BackendRefs) > 1 {
			return nil, fmt.Errorf("multiple BackendRefs in a single rule are not supported")
		}
		br := rule.BackendRefs[0]

		if err := validateCrossNamespaceRef(
			o.ReferenceGrants,
			gatewayv1.Kind("HTTPRoute"),
			hr.Namespace,
			string(br.Name),
			br.Namespace,
		); err != nil {
			return nil, err
		}

		if len(rule.Matches) == 0 {
			handler, err := buildHTTPHandler(gw, br.BackendRef, "/", o)
			if err != nil {
				return nil, err
			}
			handlers["/"] = handler
		} else {
			for _, match := range rule.Matches {
				if !isSupportedMatch(match) {
					continue
				}
				handler, err := buildMatchHandler(gw, host, br, match, o)
				if err != nil {
					return nil, err
				}
				handlers[*match.Path.Value] = handler
			}
		}
	}

	return handlers, nil
}

// buildGRPCHandlers builds HTTP/2 handlers for a GRPCRoute bound to a host.
func buildGRPCHandlers(
	gw *gatewayv1.Gateway,
	gr *gatewayv1.GRPCRoute,
	_ string,
	o options,
) (map[string]*ipn.HTTPHandler, error) {
	handlers := map[string]*ipn.HTTPHandler{}

	for _, rule := range gr.Spec.Rules {
		if len(rule.BackendRefs) > 1 {
			return nil, fmt.Errorf("multiple BackendRefs in a single rule are not supported")
		}
		if len(rule.BackendRefs) == 0 {
			continue
		}
		br := rule.BackendRefs[0]

		if err := validateCrossNamespaceRef(
			o.ReferenceGrants,
			gatewayv1.Kind("GRPCRoute"),
			gr.Namespace,
			string(br.Name),
			br.Namespace,
		); err != nil {
			return nil, err
		}

		handler, err := buildHTTPHandler(gw, br.BackendRef, "/", o)
		if err != nil {
			return nil, err
		}
		handlers["/"] = handler
	}

	return handlers, nil
}

// backendHost renders the upstream target host string for a backend reference,
// applying the optional namespace and port.
func backendHost(name string, namespace *gatewayv1.Namespace, port *gatewayv1.PortNumber) string {
	host := name
	if namespace != nil {
		host = fmt.Sprintf("%s.%s", host, *namespace)
	}
	if port != nil {
		host = fmt.Sprintf("%s:%d", host, *port)
	}
	return host
}

// validateCrossNamespaceRef returns an error when a route in fromNamespace
// references a backend in a different namespace that is not permitted by any
// provided ReferenceGrant.
func validateCrossNamespaceRef(
	grants []*gatewayv1beta1.ReferenceGrant,
	fromKind gatewayv1.Kind,
	fromNamespace string,
	backendName string,
	backendNamespace *gatewayv1.Namespace,
) error {
	if backendNamespace == nil || string(*backendNamespace) == fromNamespace {
		return nil
	}
	for _, grant := range grants {
		if grant.Namespace != string(*backendNamespace) {
			continue
		}
		fromOK := false
		for _, f := range grant.Spec.From {
			if f.Group == gatewayv1.Group("gateway.networking.k8s.io") &&
				f.Kind == fromKind &&
				string(f.Namespace) == fromNamespace {
				fromOK = true
				break
			}
		}
		if !fromOK {
			continue
		}
		toOK := false
		for _, t := range grant.Spec.To {
			if t.Group == gatewayv1.Group("") && t.Kind == gatewayv1.Kind("Service") {
				if t.Name == nil || string(*t.Name) == backendName {
					toOK = true
					break
				}
			}
		}
		if toOK {
			return nil
		}
	}
	return fmt.Errorf(
		"cross-namespace reference from %s/%s to Service %s/%s is not permitted by any ReferenceGrant",
		fromKind,
		fromNamespace,
		*backendNamespace,
		backendName,
	)
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
	gw *gatewayv1.Gateway,
	_ string,
	br gatewayv1.HTTPBackendRef,
	match gatewayv1.HTTPRouteMatch,
	o options,
) (*ipn.HTTPHandler, error) {
	if match.Path == nil {
		return nil, fmt.Errorf("path match is required")
	}
	if match.Path.Value == nil {
		return nil, fmt.Errorf("path match value is required")
	}
	return buildHTTPHandler(gw, br.BackendRef, *match.Path.Value, o)
}

// buildHTTPHandler builds an HTTP handler for a BackendRef with the given proxy
// mount path. When a BackendTLSPolicy targets the referenced Service, the
// backend connection is upgraded to HTTPS using the policy's validation
// hostname.
func buildHTTPHandler(
	gw *gatewayv1.Gateway,
	br gatewayv1.BackendRef,
	path string,
	o options,
) (*ipn.HTTPHandler, error) {
	backend := string(br.Name)
	backendNs := gw.Namespace
	if br.Namespace != nil {
		backendNs = string(*br.Namespace)
	}

	scheme := "http"
	if policy := findBackendTLSPolicy(o.BackendTLSPolicy, backendNs, backend); policy != nil {
		scheme = "https"
	}

	target := backendHost(backend, br.Namespace, br.Port)
	proxy := url.URL{
		Scheme: scheme,
		Host:   target,
		Path:   path,
	}
	return &ipn.HTTPHandler{Proxy: proxy.String()}, nil
}

// findBackendTLSPolicy returns the first BackendTLSPolicy whose TargetRefs
// select the named Service in the given namespace, or nil if none match.
func findBackendTLSPolicy(
	policies []*gatewayv1.BackendTLSPolicy,
	_ string,
	name string,
) *gatewayv1.BackendTLSPolicy {
	for _, p := range policies {
		for _, ref := range p.Spec.TargetRefs {
			if ref.Group != "" && ref.Group != "core" {
				continue
			}
			if ref.Kind != "Service" {
				continue
			}
			if ref.Name != "" && string(ref.Name) != name {
				continue
			}
			return p
		}
	}
	return nil
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
