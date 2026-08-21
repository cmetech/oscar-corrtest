package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates/*.html.tmpl static
var assets embed.FS

var parsedTemplates = template.Must(template.New("root").Funcs(template.FuncMap{
	"stringSlice": func(values ...string) []string { return values },
	"duration":    func(value time.Duration) string { return value.String() },
	"logTime": func(value time.Time) string {
		return value.UTC().Format("2006-01-02 15:04:05.000Z")
	},
	"jsonValue": func(value any) string {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "unavailable"
		}
		return string(encoded)
	},
	"jsonPretty": func(value string) string {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, []byte(value), "", "  "); err != nil {
			return value
		}
		return formatted.String()
	},
}).ParseFS(assets, "templates/*.html.tmpl"))

var staticHandler = func() http.Handler {
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(staticFS))
}()
