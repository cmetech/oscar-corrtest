package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html.tmpl static
var assets embed.FS

var parsedTemplates = template.Must(template.New("root").Funcs(template.FuncMap{
	"stringSlice": func(values ...string) []string { return values },
}).ParseFS(assets, "templates/*.html.tmpl"))

var staticHandler = func() http.Handler {
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(staticFS))
}()
