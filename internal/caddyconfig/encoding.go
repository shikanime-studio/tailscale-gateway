package caddyconfig

import (
	"bytes"
	"text/template"
)

var caddyfileTmpl = template.Must(template.New("Caddyfile").Parse(
	`{{- range .Sites }}
{{ .Address }} {
    {{- if .Upstreams }}
    reverse_proxy{{ range .Upstreams }} {{ . }}{{ end }}
    {{- end }}
    {{- range $i, $r := .Routes }}
    @r{{$i}} {
        {{- range $r.Paths }}
        {{- if eq .Type "PathPrefix" }}path {{ .Value }}*{{ else if eq .Type "PathExact" }}path {{ .Value }}{{ else }}path {{ .Value }}*{{ end }}
        {{- end }}
    }
    reverse_proxy @r{{$i}}{{ range $r.Upstreams }} {{ . }}{{ end }}
    {{- end }}
}
{{ end }}`,
))

// Marshal renders the Config into a Caddyfile and returns its bytes.
func Marshal(cfg *Config) ([]byte, error) {
	var buf bytes.Buffer
	if err := caddyfileTmpl.Execute(&buf, cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
