package caddyconfig

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type Config struct {
	Sites []Site
}

type Site struct {
	Address   string
	Upstreams []string
	Routes    []Route
}

type Route struct {
	Paths     []PathMatch
	Upstreams []string
}

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
				ruleUps := []string{}
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
					for i := int32(0); i < w; i++ {
						ruleUps = append(ruleUps, addr)
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
							for _, u := range ruleUps {
								r.Upstreams = append(r.Upstreams, u)
							}
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
								for _, u := range ruleUps {
									r.Upstreams = append(r.Upstreams, u)
								}
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
								for _, u := range ruleUps {
									r.Upstreams = append(r.Upstreams, u)
								}
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
									for _, u := range ruleUps {
										r.Upstreams = append(r.Upstreams, u)
									}
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
