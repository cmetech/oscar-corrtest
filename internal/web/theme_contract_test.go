package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestThemeDefinesApprovedTokens(t *testing.T) {
	css := readAsset(t, "static/css/tokens.css")
	dark := cssBlock(t, css, `:root`)
	light := cssBlock(t, css, `[data-theme="light"]`)

	want := map[string]map[string]string{
		"dark": {
			"--ct-bg": "#1A1D24", "--ct-card": "#242832", "--ct-card-hover": "#2C3140",
			"--ct-header": "#1E2128", "--ct-border": "#2D3340", "--ct-fg": "#FAFAFA",
			"--ct-fg-muted": "#9CA3AF", "--ct-accent": "#FAD22D", "--ct-accent-fg": "#1A1D24",
			"--ct-pass": "#0FC373", "--ct-warning": "#FF8C0A", "--ct-fail": "#FF3232",
			"--ct-activity": "#AF78D2", "color-scheme": "dark",
		},
		"light": {
			"--ct-bg": "#F7F8FA", "--ct-card": "#FFFFFF", "--ct-card-hover": "#F1F3F7",
			"--ct-header": "#1E2128", "--ct-border": "#E5E7EB", "--ct-fg": "#1A1D24",
			"--ct-fg-muted": "#4B5563", "--ct-accent": "#FAD22D", "--ct-accent-fg": "#1A1D24",
			"--ct-pass": "#087A49", "--ct-warning": "#A34D00", "--ct-fail": "#B42318",
			"--ct-activity": "#704099", "color-scheme": "light",
		},
	}

	for theme, declarations := range want {
		block := dark
		if theme == "light" {
			block = light
		}
		for property, value := range declarations {
			pattern := regexp.MustCompile(regexp.QuoteMeta(property) + `\s*:\s*` + regexp.QuoteMeta(value) + `\s*;`)
			if !pattern.MatchString(block) {
				t.Errorf("%s theme missing %s: %s", theme, property, value)
			}
		}
	}
}

func TestThemeIncludesAccessibleMotionAndFocusContracts(t *testing.T) {
	tokens := readAsset(t, "static/css/tokens.css")
	base := readAsset(t, "static/css/base.css")
	components := readAsset(t, "static/css/components.css")
	all := tokens + base + components

	for _, fragment := range []string{
		"--ct-space-1: 4px", "--ct-space-2: 8px", "--ct-space-3: 12px",
		"--ct-space-4: 16px", "--ct-space-6: 24px", "--ct-space-8: 32px",
		"--ct-radius-input: 4px", "--ct-radius-card: 6px",
		"outline: 3px solid", "@media (prefers-reduced-motion: reduce)",
	} {
		if !strings.Contains(all, fragment) {
			t.Errorf("presentation contract missing %q", fragment)
		}
	}
}

func TestThemeToggleHasStableAccessibleState(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	body := res.Body.String()
	for _, fragment := range []string{
		`data-theme-toggle`, `aria-label="Light theme"`, `aria-pressed="false"`, `data-theme-icon`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("dashboard missing %q", fragment)
		}
	}

	js := readAsset(t, "static/js/theme.js")
	for _, fragment := range []string{
		"document.documentElement.dataset.theme", "document.documentElement.style.colorScheme",
		"setAttribute('aria-pressed'", "setAttribute('aria-label', 'Light theme')",
		"localStorage.setItem('corrtest-theme'", "data-theme-icon",
	} {
		if !strings.Contains(js, fragment) {
			t.Errorf("theme script missing %q", fragment)
		}
	}
}

func TestAuthoringWorkspaceKeepsCoreControlsServerRendered(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/authoring?pattern=sequence&level=advanced", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, fragment := range []string{
		`class="authoring-workspace"`, `data-copy-target="authoring-yaml"`, `data-schema-filter`,
		`/static/js/authoring.js`, `aria-current="step"`,
	} {
		if !strings.Contains(res.Body.String(), fragment) {
			t.Errorf("authoring page missing %q", fragment)
		}
	}

	pages := readAsset(t, "static/css/pages.css")
	components := readAsset(t, "static/css/components.css")
	for _, fragment := range []string{
		".authoring-workspace", ".authoring-instrument", "@media (min-width: 1101px)", "position: sticky",
		".primary-button:focus-visible", "outline-offset: 3px",
	} {
		if !strings.Contains(pages+components, fragment) {
			t.Errorf("authoring presentation missing %q", fragment)
		}
	}
}

func TestAuthoringCookbookSelectionRequiresExplicitLoad(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/authoring?section=patterns&pattern=sequence&level=advanced&view=api", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	formStart := strings.Index(body, `<form class="authoring-controls"`)
	if formStart < 0 {
		t.Fatal("authoring page is missing the cookbook selection form")
	}
	formEnd := strings.Index(body[formStart:], `</form>`)
	if formEnd < 0 {
		t.Fatal("authoring page has an unterminated cookbook selection form")
	}
	form := body[formStart : formStart+formEnd]
	for _, fragment := range []string{
		`aria-label="Cookbook example selection"`, `action="/authoring#patterns"`,
		`name="section" value="patterns"`, `name="pattern"`, `name="level"`,
		`name="view" value="api"`, `Load cookbook example`,
	} {
		if !strings.Contains(form, fragment) {
			t.Errorf("cookbook selector form missing %q", fragment)
		}
	}
	if strings.Contains(form, `<select name="view"`) {
		t.Error("cookbook selector duplicates the inspection-panel view navigation")
	}
	if strings.Contains(body, `data-authoring-example-select`) {
		t.Error("cookbook selectors still trigger implicit page navigation")
	}
	patternsStart := strings.Index(body, `id="patterns"`)
	if patternsStart < 0 {
		t.Fatal("authoring page is missing the Pattern cookbook section")
	}
	if formStart < patternsStart {
		t.Error("cookbook selector is rendered before the Pattern cookbook it controls")
	}
}

func TestAuthoringQuickstartNavigationRestoresFixedFloodTutorial(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/authoring?section=patterns&step=assertions&pattern=sequence&level=advanced&view=api", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, href := range []string{
		`href="/authoring?section=quickstart&amp;step=identity&amp;pattern=flood&amp;level=basic&amp;view=yaml#quickstart"`,
		`href="/authoring?section=quickstart&amp;step=cases&amp;pattern=flood&amp;level=basic&amp;view=yaml#step-cases"`,
	} {
		if !strings.Contains(res.Body.String(), href) {
			t.Errorf("Quickstart navigation does not restore its fixed Flood walkthrough: missing %s", href)
		}
	}
}

func TestAuthoringWorkspaceContainsWideSchemaInsideTheCenterColumn(t *testing.T) {
	pages := readAsset(t, "static/css/pages.css")
	content := cssBlock(t, pages, `.authoring-content`)
	for _, fragment := range []string{"grid-template-columns: minmax(0, 1fr)", "min-width: 0"} {
		if !strings.Contains(content, fragment) {
			t.Errorf("authoring content does not constrain its implicit grid track with %q", fragment)
		}
	}
	directChildren := cssBlock(t, pages, `.authoring-content > *`)
	if !strings.Contains(directChildren, "min-width: 0") {
		t.Error("authoring content children can expand the center track beyond its assigned column")
	}
	schemaWrapper := cssBlock(t, pages, `.authoring-schema .data-table-wrapper`)
	for _, fragment := range []string{"width: 100%", "max-width: 100%"} {
		if !strings.Contains(schemaWrapper, fragment) {
			t.Errorf("authoring schema wrapper does not own horizontal overflow with %q", fragment)
		}
	}
}

func readAsset(t *testing.T, path string) string {
	t.Helper()
	data, err := assets.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func cssBlock(t *testing.T, css, selector string) string {
	t.Helper()
	pattern := regexp.MustCompile(regexp.QuoteMeta(selector) + `\s*\{([^}]*)\}`)
	match := pattern.FindStringSubmatch(css)
	if len(match) != 2 {
		t.Fatalf("CSS selector %q not found", selector)
	}
	return match[1]
}
