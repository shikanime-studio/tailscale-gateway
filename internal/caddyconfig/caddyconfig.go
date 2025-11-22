// Package caddyconfig models and generates Caddyfile configuration from Gateway API resources.
package caddyconfig

import (
	"fmt"
	"sort"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Config represents a Caddyfile configuration consisting of multiple sites.
type Config struct {
    Sites []Site
}

// Site represents a single Caddy virtual host and its upstream backends.
type Site struct {
    Address   string
    Upstreams []string
}

// Marshal serializes the Config to a Caddyfile by calling Marshal.
func (c *Config) Marshal() ([]byte, error) { return Marshal(c) }

// ConfigMapName returns the name of the ConfigMap that stores the Caddyfile.
func ConfigMapName(gw *gatewayv1.Gateway) string {
	return fmt.Sprintf("%s-caddy", gw.Name)
}

type options struct {
	HTTPRoutes []*gatewayv1.HTTPRoute
	Host       string
}

// Option modifies options used to build a Caddyfile Config.
type Option func(*options)

// makeOptions applies a series of Option functions to options.
func makeOptions(opts []Option) options {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithHTTPRoutes adds multiple HTTPRoutes to the options.
func WithHTTPRoutes(hrs []gatewayv1.HTTPRoute) Option {
	return func(o *options) {
		for i := range hrs {
			o.HTTPRoutes = append(o.HTTPRoutes, &hrs[i])
		}
	}
}

// WithHost sets the optional DNS suffix to append to hostnames.
func WithHost(name string) Option {
	return func(o *options) { o.Host = name }
}

// NewConfig builds a Caddyfile model from Gateway + HTTPRoutes.
func NewConfig(gw *gatewayv1.Gateway, opts ...Option) (*Config, error) {
	o := makeOptions(opts)

	if gw == nil {
		return nil, fmt.Errorf("gateway is nil")
	}

	sites := map[string]map[string]struct{}{}
	for _, l := range gw.Spec.Listeners {
		for _, hr := range o.HTTPRoutes {
			// Only include routes that reference this gateway
			parentOK := false
			for _, pr := range hr.Spec.ParentRefs {
				if string(pr.Name) == string(gw.Name) {
					if pr.Namespace == nil || string(*pr.Namespace) == string(gw.Namespace) {
						parentOK = true
						break
					}
				}
			}
			if !parentOK {
				continue
			}

			upstreams := []string{}
			for _, rrule := range hr.Spec.Rules {
				for _, br := range rrule.BackendRefs {
					if br.Port == nil {
						continue
					}
					ns := hr.Namespace
					if br.Namespace != nil {
						ns = string(*br.Namespace)
					}
					upstreams = append(
						upstreams,
						fmt.Sprintf("%s.%s:%d", br.Name, ns, *br.Port),
					)
				}
			}
			if len(upstreams) == 0 {
				continue
			}

			if len(hr.Spec.Hostnames) == 0 {
				baseAddr := fmt.Sprintf(":%d", l.Port)
				if _, ok := sites[baseAddr]; !ok {
					sites[baseAddr] = map[string]struct{}{}
				}
				for _, u := range upstreams {
					sites[baseAddr][u] = struct{}{}
				}

				if o.Host != "" {
					hostAddr := fmt.Sprintf(
						"%s-%s.%s:%d",
						gw.Namespace,
						string(gw.Name),
						o.Host,
						l.Port,
					)
					if _, ok := sites[hostAddr]; !ok {
						sites[hostAddr] = map[string]struct{}{}
					}
					for _, u := range upstreams {
						sites[hostAddr][u] = struct{}{}
					}
				}
			} else {
				for _, h := range hr.Spec.Hostnames {
					rawHost := string(h)
					addr := fmt.Sprintf("%s:%d", rawHost, l.Port)
					if _, ok := sites[addr]; !ok {
						sites[addr] = map[string]struct{}{}
					}
					for _, u := range upstreams {
						sites[addr][u] = struct{}{}
					}

					if o.Host != "" {
						suffixed := fmt.Sprintf("%s.%s:%d", rawHost, o.Host, l.Port)
						if _, ok := sites[suffixed]; !ok {
							sites[suffixed] = map[string]struct{}{}
						}
						for _, u := range upstreams {
							sites[suffixed][u] = struct{}{}
						}
					}
				}
			}
		}
	}

	// Convert to deterministic slice with sorted upstreams
	out := &Config{}
	for addr, set := range sites {
		ups := make([]string, 0, len(set))
		for u := range set {
			ups = append(ups, u)
		}
		sort.Strings(ups)
		out.Sites = append(out.Sites, Site{Address: addr, Upstreams: ups})
	}
	sort.Slice(
		out.Sites,
		func(i, j int) bool { return out.Sites[i].Address < out.Sites[j].Address },
	)

	return out, nil
}
