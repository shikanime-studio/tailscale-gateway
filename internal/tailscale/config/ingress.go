package config

import (
	"fmt"
	"net/url"

	networkingv1 "k8s.io/api/networking/v1"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
	"tailscale.com/util/dnsname"
)

// WithIngresses adds multiple Ingress resources to service options.
func WithIngresses(ingresses []*networkingv1.Ingress) Option {
	return func(o *options) {
		o.Ingresses = ingresses
	}
}

// buildIngressServices adds Tailscale services derived from Ingress rules.
func buildIngressServices(cfg *Config, ingresses []*networkingv1.Ingress) error {
	for _, ing := range ingresses {
		if err := addIngressToConfig(cfg, ing); err != nil {
			return err
		}
	}
	return nil
}

// addIngressToConfig adds a single Ingress to the Tailscale config.
func addIngressToConfig(cfg *Config, ing *networkingv1.Ingress) error {
	if len(ing.Spec.Rules) == 0 && ing.Spec.DefaultBackend == nil {
		return nil
	}

	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		var hosts []string
		if rule.Host != "" {
			hosts = []string{rule.Host}
		} else {
			hosts = []string{ing.Name}
		}
		for _, path := range rule.HTTP.Paths {
			svcName := ingressServiceName(ing, rule.Host, path.Path)
			if _, ok := cfg.cfg.Services[svcName]; ok {
				continue
			}
			svc, err := newIngressServiceConfig(ing, hosts, path)
			if err != nil {
				return err
			}
			cfg.cfg.Services[svcName] = svc
		}
	}

	if ing.Spec.DefaultBackend != nil {
		sanitized := dnsname.SanitizeLabel("ing-" + ing.Name + "-default")
		svcName := tailcfg.AsServiceName("svc:" + sanitized)
		if _, ok := cfg.cfg.Services[svcName]; !ok {
			svc, err := newIngressDefaultServiceConfig(ing)
			if err != nil {
				return err
			}
			if svc != nil {
				cfg.cfg.Services[svcName] = svc
			}
		}
	}

	return nil
}

// ingressServiceName returns a stable service name for an Ingress path.
func ingressServiceName(ing *networkingv1.Ingress, host, path string) tailcfg.ServiceName {
	if host == "" {
		host = "_wildcard"
	}
	cleanPath := path
	if cleanPath == "" {
		cleanPath = "/"
	}
	raw := fmt.Sprintf("ing-%s-%s-%s", ing.Name, host, cleanPath)
	sanitized := dnsname.SanitizeLabel(raw)
	return tailcfg.AsServiceName("svc:" + sanitized)
}

// newIngressServiceConfig builds a Tailscale service config from an Ingress path.
func newIngressServiceConfig(
	ing *networkingv1.Ingress,
	hosts []string,
	path networkingv1.HTTPIngressPath,
) (*ipn.ServiceConfig, error) {
	backend := path.Backend
	if backend.Service == nil {
		return nil, fmt.Errorf("non-service backends not supported")
	}
	port, err := backendPortNumber(backend.Service.Port)
	if err != nil {
		return nil, err
	}

	web := map[ipn.HostPort]*ipn.WebServerConfig{}
	proxyURL := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", backend.Service.Name, ing.Namespace, port),
		Path:   "/",
	}

	pathStr := path.Path
	if pathStr == "" {
		pathStr = "/"
	}

	for _, host := range hosts {
		hp := ipn.HostPort(fmt.Sprintf("%s:%d", host, 80))
		web[hp] = &ipn.WebServerConfig{
			Handlers: map[string]*ipn.HTTPHandler{
				pathStr: {Proxy: proxyURL.String()},
			},
		}
	}

	return &ipn.ServiceConfig{
		TCP: newIngressTCPPortHandlers(),
		Web: web,
	}, nil
}

// newIngressDefaultServiceConfig builds a Tailscale service config from an Ingress default backend.
func newIngressDefaultServiceConfig(ing *networkingv1.Ingress) (*ipn.ServiceConfig, error) {
	backend := ing.Spec.DefaultBackend
	if backend == nil {
		return nil, nil
	}
	if backend.Service == nil {
		return nil, nil
	}
	port, err := backendPortNumber(backend.Service.Port)
	if err != nil {
		return nil, err
	}

	hosts := []string{ing.Name}
	web := map[ipn.HostPort]*ipn.WebServerConfig{}
	hp := ipn.HostPort(fmt.Sprintf("%s:%d", hosts[0], 80))
	proxyURL := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", backend.Service.Name, ing.Namespace, port),
		Path:   "/",
	}
	web[hp] = &ipn.WebServerConfig{
		Handlers: map[string]*ipn.HTTPHandler{
			"/": {Proxy: proxyURL.String()},
		},
	}
	return &ipn.ServiceConfig{
		TCP: newIngressTCPPortHandlers(),
		Web: web,
	}, nil
}

// newIngressTCPPortHandlers returns TCP handlers for HTTP on port 80.
func newIngressTCPPortHandlers() map[uint16]*ipn.TCPPortHandler {
	return map[uint16]*ipn.TCPPortHandler{
		80: {HTTP: true},
	}
}

// backendPortNumber resolves the port number from a ServiceBackendPort.
func backendPortNumber(port networkingv1.ServiceBackendPort) (int32, error) {
	if port.Number != 0 {
		return port.Number, nil
	}
	if port.Name != "" {
		return 0, fmt.Errorf("named ports not supported: %q", port.Name)
	}
	return 0, fmt.Errorf("port must be specified")
}
