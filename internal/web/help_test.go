package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestEveryConsolePageHasCompleteHelp(t *testing.T) {
	catalog := defaultHelpCatalog()
	for _, page := range []string{"dashboard", "targets", "run-test", "scenarios", "runs", "run-detail", "operations", "reference"} {
		topic, ok := catalog.Topic(page)
		if !ok {
			t.Errorf("missing topic %q", page)
			continue
		}
		seen := map[string]bool{}
		for _, section := range topic.Sections {
			seen[strings.ToLower(section.Heading)] = true
		}
		for _, heading := range []string{"purpose", "workflow", "oscar effect", "evidence", "cli equivalent"} {
			if !seen[heading] {
				t.Errorf("topic %q missing %q", page, heading)
			}
		}
	}
}

func TestHelpCatalogDocumentsAllPatternsAndLabels(t *testing.T) {
	text := ""
	for _, topic := range defaultHelpCatalog().All() {
		text += topic.ID + topic.Title + topic.Summary
		for _, section := range topic.Sections {
			text += section.Heading + strings.Join(section.Paragraphs, " ") + strings.Join(section.Bullets, " ") + section.Code
		}
	}
	for _, required := range []string{"co_occurrence", "flood", "sequence", "persistence", "absence", "parent_child", "cross_source", "threshold", "notifier", "oscar_test_run_id", "P01", "N01", "service status"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(required)) {
			t.Errorf("catalog missing %q", required)
		}
	}
}

func TestReferenceRouteAndContextualDrawer(t *testing.T) {
	handler := NewHandler(version.Info{Version: "test"})
	for _, path := range []string{"/", "/reference"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, response.Code)
		}
		body := response.Body.String()
		for _, required := range []string{`aria-controls="page-help-drawer"`, `role="dialog"`, `/reference#`, `src="/static/js/help.js"`} {
			if !strings.Contains(body, required) {
				t.Errorf("GET %s missing %q", path, required)
			}
		}
	}
}
