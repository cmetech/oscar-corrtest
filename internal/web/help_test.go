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
	for _, page := range []string{"dashboard", "targets", "run-test", "scenarios", "authoring", "runs", "run-detail", "operations", "reference"} {
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

func TestHelpCatalogLinksUseLabeledKnownSameOriginRoutes(t *testing.T) {
	validTopics := []HelpTopic{{ID: "scenarios", Title: "Scenarios", Summary: "Summary", Links: []HelpLink{{Label: "Authoring", Href: "/authoring?section=quickstart"}}}}
	if _, err := newHelpCatalog(validTopics); err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		link HelpLink
	}{
		{"blank-label", HelpLink{Href: "/authoring"}},
		{"unknown-route", HelpLink{Label: "Unknown", Href: "/not-a-console-route"}},
		{"off-site", HelpLink{Label: "Off-site", Href: "https://example.com/authoring"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := newHelpCatalog([]HelpTopic{{ID: "topic", Title: "Topic", Summary: "Summary", Links: []HelpLink{test.link}}})
			if err == nil {
				t.Fatal("invalid help link was accepted")
			}
		})
	}
}

func TestReferenceAndContextHelpLinkToAuthoringSectionsAndPatterns(t *testing.T) {
	handler := NewHandler(version.Info{Version: "test"})
	for _, path := range []string{"/reference", "/authoring"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, href := range []string{
			"/authoring?section=quickstart", "/authoring?section=schema", "/authoring?section=assertions", "/authoring?section=validation",
		} {
			if !strings.Contains(body, href) {
				t.Errorf("GET %s missing %q", path, href)
			}
		}
	}
	scenarios := httptest.NewRecorder()
	NewHandlerWithData(version.Info{Version: "test"}, &scenarioUIData{}).ServeHTTP(scenarios, httptest.NewRequest(http.MethodGet, "/scenarios", nil))
	if scenarios.Code != http.StatusOK {
		t.Fatalf("GET /scenarios status=%d body=%s", scenarios.Code, scenarios.Body.String())
	}
	for _, want := range []string{"Open the Scenario Authoring Guide", "Browse the public YAML schema", "/reference#scenarios"} {
		if !strings.Contains(scenarios.Body.String(), want) {
			t.Errorf("Scenarios context help missing %q", want)
		}
	}
	authoring := httptest.NewRecorder()
	handler.ServeHTTP(authoring, httptest.NewRequest(http.MethodGet, "/authoring", nil))
	for _, want := range []string{"Open Scenarios", "Quickstart", "Assertions", "Validation", "/reference#authoring"} {
		if !strings.Contains(authoring.Body.String(), want) {
			t.Errorf("Authoring context help missing %q", want)
		}
	}

	reference := httptest.NewRecorder()
	handler.ServeHTTP(reference, httptest.NewRequest(http.MethodGet, "/reference", nil))
	for _, pattern := range []string{"co_occurrence", "flood", "sequence", "persistence", "absence", "parent_child", "cross_source", "threshold"} {
		if !strings.Contains(reference.Body.String(), "/authoring?section=patterns&amp;pattern="+pattern) {
			t.Errorf("reference missing authoring pattern %q", pattern)
		}
	}
	if !strings.Contains(reference.Body.String(), "Open full reference") {
		t.Fatal("context help lost its Reference fallback")
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

func TestEveryPageIncludesAccessibleBrandedConfirmationDialog(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(version.Info{Version: "test"}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, required := range []string{
		`<dialog class="confirm-dialog"`, `data-confirm-dialog`,
		`aria-modal="true"`,
		`aria-labelledby="confirm-dialog-title"`, `aria-describedby="confirm-dialog-description"`,
		`data-confirm-cancel`, `data-confirm-accept`, `src="/static/js/confirm-dialog.js"`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("branded confirmation dialog missing %q", required)
		}
	}
}
