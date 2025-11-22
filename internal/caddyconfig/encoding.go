package caddyconfig

import (
	"bytes"
	"text/template"
)

var caddyfileTmpl = template.Must(template.New("Caddyfile").Parse(
	`{{- range .Sites }}
{{ .Address }} {
    {{- range $r := .Routes }}
        {{- range $r.Paths }}
        {{- if eq .Type "PathExact" }}
    handle {{ .Value }} {
        reverse_proxy{{ range $r.Upstreams }} {{ . }}{{ end }}
    }
        {{- else }}
    handle_path {{ .Value }} {
        reverse_proxy{{ range $r.Upstreams }} {{ . }}{{ end }}
    }
        {{- end }}
        {{- end }}
    {{- end }}
    {{- if .Upstreams }}
    handle {
        reverse_proxy{{ range .Upstreams }} {{ . }}{{ end }}
    }
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
