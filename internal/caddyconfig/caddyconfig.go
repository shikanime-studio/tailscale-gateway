// Package caddyconfig models and generates Caddyfile configuration from Gateway API resources.
package caddyconfig

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Config represents a Caddyfile configuration consisting of multiple sites.
type Config struct {
	Sites []Site
}

// Site represents a single Caddy virtual host and its upstreams and routes.
type Site struct {
	Address   string
	Upstreams []string
	Address   string
	Upstreams []string
	Routes    []Route
}

// Route describes path-based routing to upstream backends.
type Route struct {
	Paths     []PathMatch
	Upstreams []string
}

// PathMatch specifies a path match type and value.
type PathMatch struct {
	Type  string
	Value string
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

	type agg struct {
		defaults map[string]struct{}
		routes   []Route
	}
	sites := map[string]*agg{}
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

			for _, rrule := range hr.Spec.Rules {
				// Collect upstream weights
				type up struct {
					addr string
					w    int32
				}
				upsWeighted := []up{}
				for _, br := range rrule.BackendRefs {
					if br.Port == nil {
						continue
					}
					ns := hr.Namespace
					if br.Namespace != nil {
						ns = string(*br.Namespace)
					}
					addr := fmt.Sprintf("%s.%s:%d", br.Name, ns, *br.Port)
					w := int32(1)
					if br.Weight != nil {
						w = *br.Weight
						if w < 1 {
							w = 1
						}
					}
					upsWeighted = append(upsWeighted, up{addr: addr, w: w})
				}
				// Scale weights down by GCD to avoid excessive duplication
				ruleUps := []string{}
				if len(upsWeighted) > 0 {
					// Compute gcd across weights
					gcd := upsWeighted[0].w
					for i := 1; i < len(upsWeighted); i++ {
						// Euclidean algorithm
						a, b := gcd, upsWeighted[i].w
						for b != 0 {
							a, b = b, a%b
						}
						gcd = a
					}
					if gcd < 1 {
						gcd = 1
					}
					// Cap total duplicates to a sane limit while keeping ratio
					const maxTotal = int32(32)
					var sumScaled int32
					for _, u := range upsWeighted {
						sumScaled += u.w / gcd
					}
					scale := int32(1)
					if sumScaled > maxTotal && sumScaled > 0 {
						// further scale down proportionally
						scale = (sumScaled + maxTotal - 1) / maxTotal
						if scale < 1 {
							scale = 1
						}
					}
					for _, u := range upsWeighted {
						count := u.w / gcd / scale
						if count < 1 {
							count = 1
						}
						for i := int32(0); i < count; i++ {
							ruleUps = append(ruleUps, u.addr)
						}
					}
				}
				if len(ruleUps) == 0 {
					continue
				}

				if len(hr.Spec.Hostnames) == 0 {
					baseAddr := fmt.Sprintf(":%d", l.Port)
					if _, ok := sites[baseAddr]; !ok {
						sites[baseAddr] = &agg{defaults: map[string]struct{}{}, routes: []Route{}}
					}
					if len(rrule.Matches) == 0 {
						for _, u := range ruleUps {
							sites[baseAddr].defaults[u] = struct{}{}
						}
					} else {
						r := Route{}
						for _, m := range rrule.Matches {
							if m.Path != nil {
								pmType := "PathPrefix"
								if m.Path.Type != nil {
									pmType = string(*m.Path.Type)
								}
								if m.Path.Value != nil {
									r.Paths = append(r.Paths, PathMatch{Type: pmType, Value: *m.Path.Value})
								}
							}
						}
						if len(r.Paths) > 0 {
							r.Upstreams = append(r.Upstreams, ruleUps...)
							sites[baseAddr].routes = append(sites[baseAddr].routes, r)
						} else {
							for _, u := range ruleUps {
								sites[baseAddr].defaults[u] = struct{}{}
							}
						}
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
							sites[hostAddr] = &agg{
								defaults: map[string]struct{}{},
								routes:   []Route{},
							}
						}
						if len(rrule.Matches) == 0 {
							for _, u := range ruleUps {
								sites[hostAddr].defaults[u] = struct{}{}
							}
						} else {
							r := Route{}
							for _, m := range rrule.Matches {
								if m.Path != nil {
									pmType := "PathPrefix"
									if m.Path.Type != nil {
										pmType = string(*m.Path.Type)
									}
									if m.Path.Value != nil {
										r.Paths = append(r.Paths, PathMatch{Type: pmType, Value: *m.Path.Value})
									}
								}
							}
							if len(r.Paths) > 0 {
								r.Upstreams = append(r.Upstreams, ruleUps...)
								sites[hostAddr].routes = append(sites[hostAddr].routes, r)
							} else {
								for _, u := range ruleUps {
									sites[hostAddr].defaults[u] = struct{}{}
								}
							}
						}
					}
				} else {
					for _, h := range hr.Spec.Hostnames {
						rawHost := string(h)
						addr := fmt.Sprintf("%s:%d", rawHost, l.Port)
						if _, ok := sites[addr]; !ok {
							sites[addr] = &agg{defaults: map[string]struct{}{}, routes: []Route{}}
						}
						if len(rrule.Matches) == 0 {
							for _, u := range ruleUps {
								sites[addr].defaults[u] = struct{}{}
							}
						} else {
							r := Route{}
							for _, m := range rrule.Matches {
								if m.Path != nil {
									pmType := "PathPrefix"
									if m.Path.Type != nil {
										pmType = string(*m.Path.Type)
									}
									if m.Path.Value != nil {
										r.Paths = append(r.Paths, PathMatch{Type: pmType, Value: *m.Path.Value})
									}
								}
							}
							if len(r.Paths) > 0 {
								r.Upstreams = append(r.Upstreams, ruleUps...)
								sites[addr].routes = append(sites[addr].routes, r)
							} else {
								for _, u := range ruleUps {
									sites[addr].defaults[u] = struct{}{}
								}
							}
						}

						if o.Host != "" {
							suffixed := fmt.Sprintf("%s.%s:%d", rawHost, o.Host, l.Port)
							if _, ok := sites[suffixed]; !ok {
								sites[suffixed] = &agg{defaults: map[string]struct{}{}, routes: []Route{}}
							}
							if len(rrule.Matches) == 0 {
								for _, u := range ruleUps {
									sites[suffixed].defaults[u] = struct{}{}
								}
							} else {
								r := Route{}
								for _, m := range rrule.Matches {
									if m.Path != nil {
										pmType := "PathPrefix"
										if m.Path.Type != nil {
											pmType = string(*m.Path.Type)
										}
										if m.Path.Value != nil {
											r.Paths = append(r.Paths, PathMatch{Type: pmType, Value: *m.Path.Value})
										}
									}
								}
								if len(r.Paths) > 0 {
									r.Upstreams = append(r.Upstreams, ruleUps...)
									sites[suffixed].routes = append(sites[suffixed].routes, r)
								} else {
									for _, u := range ruleUps {
										sites[suffixed].defaults[u] = struct{}{}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Convert to slice without deterministic sorting
	out := &Config{}
	for addr, a := range sites {
		ups := make([]string, 0, len(a.defaults))
		for u := range a.defaults {
			ups = append(ups, u)
		}
		site := Site{Address: addr, Upstreams: ups}
		site.Routes = a.routes
		out.Sites = append(out.Sites, site)
	}

	return out, nil
}
