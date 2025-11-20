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
}
{{ end }}`,
))

func Marshal(cfg *Config) ([]byte, error) {
	var buf bytes.Buffer
	if err := caddyfileTmpl.Execute(&buf, cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
