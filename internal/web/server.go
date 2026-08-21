package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/authoring"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/operations"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

var errListenAddressRequired = errors.New("listen address is required")

// DataSource is the narrow durable history surface consumed by server-rendered pages.
type DataSource interface {
	ReadyStatus() (bool, string)
	CreateTarget(context.Context, domain.TargetInput) (domain.Target, error)
	ListTargets(context.Context) ([]domain.Target, error)
	ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error)
	GetRun(context.Context, string) (domain.Run, error)
	ListRunEvents(context.Context, string) ([]domain.RunEvent, error)
	ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error)
}

// Options configures the embedded HTTP server.
type Options struct {
	ListenAddress string
	Version       version.Info
	Data          DataSource
	Security      Security
	TLSCertFile   string
	TLSKeyFile    string
	Logger        *slog.Logger
}

type pageData struct {
	Nonce              string
	Version            version.Info
	Page               string
	Targets            []domain.Target
	Runs               []domain.Run
	Run                *domain.Run
	Events             []domain.RunEvent
	Artifacts          []domain.ArtifactEvidence
	Readiness          readinessView
	CSRFToken          string
	Error              string
	Status             string
	Verdict            string
	Cleanup            string
	Pattern            string
	Scenarios          []scenario.Scenario
	CanCancel          bool
	CanDelete          bool
	ImportedScenarios  []domain.ScenarioRecord
	ScenarioSource     string
	ScenarioPlan       *compiler.Plan
	Help               HelpTopic
	ReferenceTopics    []HelpTopic
	ScenarioCatalog    []scenarioCatalogItem
	SelectedScenario   string
	SelectedScenarioID string
	SelectedName       string
	SelectedPattern    string
	SelectedBuiltIn    bool
	SelectedDraft      bool
	Operations         operations.Snapshot
	OperationLogs      []applog.Record
	OperationMessage   string
	Authoring          *authoring.Page
	AuthoringFilter    string
	AuthoringFields    []scenario.FieldDefinition
}

type readinessView struct {
	Ready bool
	Error string
}

type nonceFunc func() (string, error)

type runStarter interface {
	StartBuiltin(context.Context, string, string, string) (domain.Run, error)
}

type runExporter interface {
	ExportRun(context.Context, string, string) (evidence.Result, error)
}

type runCanceller interface {
	CancelRun(context.Context, string) error
}

type runDeleter interface {
	DeleteRun(context.Context, string) error
}

type scenarioManager interface {
	ListScenarios(context.Context) ([]domain.ScenarioRecord, error)
	PreviewScenario(context.Context, string, scenario.Scenario, string) (compiler.Plan, error)
	ImportScenario(context.Context, []byte, scenario.Scenario) (domain.ScenarioRecord, error)
}

type scenarioDeleter interface {
	DeleteScenario(context.Context, string) error
}

type scenarioInspector interface {
	InspectScenario(context.Context, []byte, string) (appruntime.ScenarioInspection, error)
}

type scenarioCatalogItem struct {
	Ref      string
	Name     string
	Pattern  string
	Kind     string
	Selected bool
}

type operationsProvider interface {
	Operations() *operations.Controller
}

// NewHandler returns the Plan-1 shell with no durable data source.
func NewHandler(info version.Info) http.Handler {
	return newHandler(info, parsedTemplates, staticHandler, generateNonce)
}

// NewHandlerWithData returns the complete durable server-rendered application.
func NewHandlerWithData(info version.Info, data DataSource) http.Handler {
	return newHandlerWithData(info, data, parsedTemplates, staticHandler, generateNonce)
}

// NewHandlerWithOptions applies the explicit remote authentication policy.
func NewHandlerWithOptions(info version.Info, data DataSource, security Security) (http.Handler, error) {
	if err := security.validate(); err != nil {
		return nil, err
	}
	base := newHandlerWithData(info, data, parsedTemplates, staticHandler, generateNonce)
	if security.Mode == SecurityNone {
		return base, nil
	}
	return newAuthHandler(base, security, time.Now), nil
}

func newHandler(info version.Info, tmpl *template.Template, static http.Handler, nonce nonceFunc) http.Handler {
	return newHandlerWithData(info, nil, tmpl, static, nonce)
}

func newHandlerWithData(info version.Info, data DataSource, tmpl *template.Template, static http.Handler, nonce nonceFunc) http.Handler {
	csrfSecret := make([]byte, 32)
	_, csrfErr := rand.Read(csrfSecret)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		ready, message := readyStatus(data)
		if !ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": message})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCache(static)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		render(w, tmpl, nonce, pageData{Version: info, Page: "dashboard", Readiness: readiness(data)})
	})
	mux.HandleFunc("GET /targets", func(w http.ResponseWriter, r *http.Request) {
		view := pageData{Version: info, Page: "targets", Readiness: readiness(data)}
		if data != nil {
			targets, err := data.ListTargets(r.Context())
			if err != nil {
				view.Error = err.Error()
				renderStatus(w, tmpl, nonce, http.StatusServiceUnavailable, view)
				return
			}
			view.Targets = targets
			if csrfErr == nil {
				view.CSRFToken = csrfToken(w, r, csrfSecret)
			}
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("POST /targets", func(w http.ResponseWriter, r *http.Request) {
		if data == nil || csrfErr != nil {
			http.Error(w, "target creation unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "target form is too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid target form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		credential := domain.CredentialRef{Kind: domain.CredentialKind(r.FormValue("credential_kind")), Reference: r.FormValue("credential_ref")}
		_, err := data.CreateTarget(r.Context(), domain.TargetInput{
			DisplayName: r.FormValue("display_name"), BaseURL: r.FormValue("base_url"), APIProfile: "public-v1",
			TLS: domain.TLSPolicy{Insecure: r.FormValue("insecure") == "on", CAPath: r.FormValue("ca_path")}, Credential: credential,
		})
		if err != nil {
			targets, _ := data.ListTargets(r.Context())
			renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, pageData{
				Version: info, Page: "targets", Targets: targets, CSRFToken: csrfToken(w, r, csrfSecret), Error: err.Error(), Readiness: readiness(data),
			})
			return
		}
		http.Redirect(w, r, "/targets", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /runs", func(w http.ResponseWriter, r *http.Request) {
		view := pageData{Version: info, Page: "runs", Readiness: readiness(data)}
		view.Status = r.URL.Query().Get("status")
		view.Verdict = r.URL.Query().Get("verdict")
		view.Cleanup = r.URL.Query().Get("cleanup")
		view.Pattern = r.URL.Query().Get("pattern")
		if data != nil {
			filter := domain.RunFilter{Status: domain.RunStatus(view.Status), Verdict: domain.Verdict(view.Verdict), CleanupStatus: domain.CleanupStatus(view.Cleanup), Pattern: view.Pattern}
			if (filter.Status != "" && !filter.Status.Valid()) || (filter.Verdict != "" && !filter.Verdict.Valid()) || (filter.CleanupStatus != "" && !filter.CleanupStatus.Valid()) {
				http.Error(w, "invalid run filter", http.StatusBadRequest)
				return
			}
			runs, err := data.ListRuns(r.Context(), filter)
			if err != nil {
				view.Error = err.Error()
				renderStatus(w, tmpl, nonce, http.StatusServiceUnavailable, view)
				return
			}
			view.Runs = runs
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("GET /run-test", func(w http.ResponseWriter, r *http.Request) {
		view := pageData{Version: info, Page: "run-test", Readiness: readiness(data), Scenarios: scenario.AllBuiltins()}
		if data != nil {
			targets, err := data.ListTargets(r.Context())
			if err != nil {
				renderStatus(w, tmpl, nonce, http.StatusServiceUnavailable, pageData{Version: info, Page: "run-test", Error: err.Error(), Readiness: readiness(data)})
				return
			}
			view.Targets = targets
			if csrfErr == nil {
				view.CSRFToken = csrfToken(w, r, csrfSecret)
			}
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("GET /authoring", func(w http.ResponseWriter, r *http.Request) {
		page, err := authoring.New(info.Version).Build(authoringSelection(r.URL.Query()))
		if err != nil {
			if strings.HasPrefix(err.Error(), "invalid authoring ") {
				http.Error(w, "authoring selection is unavailable", http.StatusNotFound)
				return
			}
			http.Error(w, "authoring workspace is unavailable", http.StatusInternalServerError)
			return
		}
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		render(w, tmpl, nonce, pageData{Version: info, Page: "authoring", Authoring: &page, AuthoringFilter: filter, AuthoringFields: authoringFields(page.Contract.Fields, filter)})
	})
	mux.HandleFunc("GET /scenarios", func(w http.ResponseWriter, r *http.Request) {
		manager, ok := data.(scenarioManager)
		if !ok || csrfErr != nil {
			http.Error(w, "scenario management unavailable", http.StatusServiceUnavailable)
			return
		}
		items, err := manager.ListScenarios(r.Context())
		if err != nil {
			http.Error(w, "scenario management unavailable", http.StatusServiceUnavailable)
			return
		}
		view, err := scenarioWorkbench(r.Context(), info, data, items, r.URL.Query().Get("selected"))
		if err != nil {
			http.Error(w, "scenario selection is unavailable", http.StatusNotFound)
			return
		}
		view.CSRFToken = csrfToken(w, r, csrfSecret)
		if r.URL.Query().Get("message") == "scenario-deleted" {
			view.Status = "Custom scenario deleted from the local catalog"
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("POST /scenarios/clone", func(w http.ResponseWriter, r *http.Request) {
		_, ok := data.(scenarioManager)
		if !ok || csrfErr != nil {
			http.Error(w, "scenario management unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid clone form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		pattern, ok := strings.CutPrefix(r.FormValue("scenario_ref"), "builtin:")
		if !ok {
			http.Error(w, "only built-in scenarios can be cloned", http.StatusUnprocessableEntity)
			return
		}
		if document := scenario.Builtin(pattern); len(document.Cases) != 2 {
			http.Error(w, "built-in scenario is unavailable", http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/scenarios?selected="+url.QueryEscape("draft:"+pattern), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /scenarios", func(w http.ResponseWriter, r *http.Request) {
		manager, ok := data.(scenarioManager)
		if !ok || csrfErr != nil {
			http.Error(w, "scenario management unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid scenario form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		source := r.FormValue("source")
		items, listErr := manager.ListScenarios(r.Context())
		if listErr != nil {
			http.Error(w, "scenario management unavailable", http.StatusServiceUnavailable)
			return
		}
		view, viewErr := scenarioWorkbench(r.Context(), info, data, items, r.URL.Query().Get("selected"))
		if viewErr != nil {
			view = pageData{Version: info, Page: "scenarios", Readiness: readiness(data)}
		}
		view.ScenarioSource = source
		view.SelectedBuiltIn = false
		view.CSRFToken = csrfToken(w, r, csrfSecret)
		document, decodeErr := scenario.Decode(strings.NewReader(source))
		if decodeErr != nil {
			view.Error = decodeErr.Error()
			renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, view)
			return
		}
		view.SelectedName = document.Name
		view.SelectedPattern = document.Pattern
		switch r.FormValue("action") {
		case "preview":
			mode := r.FormValue("pipeline_mode")
			if mode != "phase_a_audit_only" && mode != "phase_b_dispatch" {
				view.Error = "pipeline mode is invalid"
				renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, view)
				return
			}
			var plan compiler.Plan
			var err error
			if inspector, ok := data.(scenarioInspector); ok {
				inspection, inspectErr := inspector.InspectScenario(r.Context(), []byte(source), mode)
				plan, err = inspection.Plan, inspectErr
			} else {
				plan, err = manager.PreviewScenario(r.Context(), r.FormValue("target_id"), document, mode)
			}
			if err != nil {
				view.Error = err.Error()
				renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, view)
				return
			}
			view.ScenarioPlan = &plan
			view.Status = "Preview compiled without contacting or mutating OSCAR"
		case "import":
			record, err := manager.ImportScenario(r.Context(), []byte(source), document)
			if err != nil {
				view.Error = err.Error()
				renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, view)
				return
			}
			selectedRef := "imported:" + record.ID
			view.SelectedScenario = selectedRef
			view.SelectedScenarioID = record.ID
			view.SelectedDraft = false
			found := false
			catalog := make([]scenarioCatalogItem, 0, len(view.ScenarioCatalog))
			for index := range view.ScenarioCatalog {
				if view.ScenarioCatalog[index].Kind == "Unsaved draft" {
					continue
				}
				view.ScenarioCatalog[index].Selected = view.ScenarioCatalog[index].Ref == selectedRef
				found = found || view.ScenarioCatalog[index].Selected
				catalog = append(catalog, view.ScenarioCatalog[index])
			}
			view.ScenarioCatalog = catalog
			if !found {
				view.ScenarioCatalog = append(view.ScenarioCatalog, scenarioCatalogItem{Ref: selectedRef, Name: record.Name, Pattern: document.Pattern, Kind: "Custom", Selected: true})
			}
			mode := r.FormValue("pipeline_mode")
			if mode != "phase_a_audit_only" && mode != "phase_b_dispatch" {
				mode = "phase_b_dispatch"
			}
			if inspector, ok := data.(scenarioInspector); ok {
				inspection, inspectErr := inspector.InspectScenario(r.Context(), []byte(source), mode)
				if inspectErr != nil {
					view.Error = inspectErr.Error()
				} else {
					view.ScenarioPlan = &inspection.Plan
				}
			}
			view.Status = "Saved custom scenario " + record.Name + " as an immutable version"
		default:
			view.Error = "scenario action is invalid"
			renderStatus(w, tmpl, nonce, http.StatusUnprocessableEntity, view)
			return
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("POST /scenarios/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		deleter, ok := data.(scenarioDeleter)
		if !ok || csrfErr != nil {
			http.Error(w, "scenario deletion unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid scenario deletion form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		if r.FormValue("confirm") != "delete" {
			http.Error(w, "scenario deletion requires explicit confirmation", http.StatusUnprocessableEntity)
			return
		}
		if err := deleter.DeleteScenario(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, "scenario was not deleted: "+err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/scenarios?message=scenario-deleted", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /runs", func(w http.ResponseWriter, r *http.Request) {
		starter, ok := data.(runStarter)
		if !ok || csrfErr != nil {
			http.Error(w, "run execution unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid run form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		mode := r.FormValue("pipeline_mode")
		if mode != "phase_a_audit_only" && mode != "phase_b_dispatch" {
			http.Error(w, "pipeline mode is invalid", http.StatusUnprocessableEntity)
			return
		}
		run, err := starter.StartBuiltin(r.Context(), r.FormValue("target_id"), r.FormValue("pattern"), mode)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/runs/"+url.PathEscape(run.ID), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if data == nil {
			http.NotFound(w, r)
			return
		}
		run, err := data.GetRun(r.Context(), r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		events, err := data.ListRunEvents(r.Context(), run.ID)
		if err != nil {
			renderStatus(w, tmpl, nonce, http.StatusServiceUnavailable, pageData{Version: info, Page: "run-detail", Run: &run, Error: err.Error(), Readiness: readiness(data)})
			return
		}
		artifacts, err := data.ListArtifactEvidence(r.Context(), run.ID)
		if err != nil {
			renderStatus(w, tmpl, nonce, http.StatusServiceUnavailable, pageData{Version: info, Page: "run-detail", Run: &run, Events: events, Error: err.Error(), Readiness: readiness(data)})
			return
		}
		view := pageData{Version: info, Page: "run-detail", Run: &run, Events: events, Artifacts: artifacts, Readiness: readiness(data)}
		_, canCancel := data.(runCanceller)
		_, canDelete := data.(runDeleter)
		view.CanCancel = canCancel && run.Status != domain.RunCompleted && run.Status != domain.RunInterrupted
		view.CanDelete = canDelete && (run.Status == domain.RunCompleted || run.Status == domain.RunInterrupted) &&
			(run.CleanupStatus == domain.CleanupClean || run.CleanupStatus == domain.CleanupNotRequired)
		if (view.CanCancel || view.CanDelete) && csrfErr == nil {
			view.CSRFToken = csrfToken(w, r, csrfSecret)
		}
		render(w, tmpl, nonce, view)
	})
	mux.HandleFunc("POST /runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		canceller, ok := data.(runCanceller)
		if !ok || csrfErr != nil {
			http.Error(w, "run cancellation unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid cancellation form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		if err := canceller.CancelRun(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/runs/"+url.PathEscape(r.PathValue("id")), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /runs/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		deleter, ok := data.(runDeleter)
		if !ok || csrfErr != nil {
			http.Error(w, "run deletion unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid deletion form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		if err := deleter.DeleteRun(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/runs", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /runs/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		exporter, ok := data.(runExporter)
		if !ok {
			http.Error(w, "evidence export unavailable", http.StatusServiceUnavailable)
			return
		}
		directory, err := os.MkdirTemp("", "oscar-corrtest-export-*")
		if err != nil {
			http.Error(w, "evidence export unavailable", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(directory)
		destination := filepath.Join(directory, "evidence.zip")
		if _, err := exporter.ExportRun(r.Context(), r.PathValue("id"), destination); err != nil {
			http.Error(w, "evidence export failed", http.StatusUnprocessableEntity)
			return
		}
		data, err := os.ReadFile(destination) // #nosec G304 -- destination is a server-generated file in a fresh private directory.
		if err != nil || len(data) > 64<<20 {
			http.Error(w, "evidence export unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="oscar-corrtest-%s.zip"`, r.PathValue("id")))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if data == nil {
			http.NotFound(w, r)
			return
		}
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil && r.URL.Query().Get("after") != "" {
			http.Error(w, "after must be an event sequence", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		disableStreamingDeadline(w)
		flusher, _ := w.(http.Flusher)
		for {
			events, listErr := data.ListRunEvents(r.Context(), r.PathValue("id"))
			if listErr != nil {
				return
			}
			for _, event := range events {
				if event.Sequence <= after {
					continue
				}
				encoded, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "id: %d\nevent: run-event\ndata: %s\n\n", event.Sequence, encoded)
				after = event.Sequence
			}
			if flusher != nil {
				flusher.Flush()
			}
			run, getErr := data.GetRun(r.Context(), r.PathValue("id"))
			if getErr != nil || run.Status == domain.RunCompleted || run.Status == domain.RunInterrupted {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	})
	mux.HandleFunc("GET /operations", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil || csrfErr != nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		snapshot, err := controller.Snapshot(r.Context())
		if err != nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		render(w, tmpl, nonce, pageData{Version: info, Page: "operations", Readiness: readiness(data), Operations: snapshot, OperationLogs: controller.RecentLogs(200), OperationMessage: r.URL.Query().Get("message"), CSRFToken: csrfToken(w, r, csrfSecret)})
	})
	mux.HandleFunc("POST /operations/api-key", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil || csrfErr != nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid API key form", http.StatusBadRequest)
			return
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		if _, err := controller.ReplaceAPIKey(r.Context(), r.FormValue("api_key")); err != nil {
			http.Error(w, "API key was not saved", http.StatusUnprocessableEntity)
			return
		}
		r.Form = nil
		r.PostForm = nil
		http.Redirect(w, r, "/operations?message=api-key-saved", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /operations/api-key/clear", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil || csrfErr != nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil || !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		if _, err := controller.ClearAPIKey(r.Context()); err != nil {
			http.Error(w, "API key was not cleared", http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/operations?message=api-key-cleared", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /operations/service/{action}", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil || csrfErr != nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil || !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return
		}
		action := r.PathValue("action")
		if action == "stop" || action == "restart" {
			actionContext, cancelAction := detachedServiceActionContext(r)
			w.Header().Set("Location", "/operations?message=service-"+action+"-requested")
			w.WriteHeader(http.StatusSeeOther)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			go func() {
				defer cancelAction()
				_, _ = controller.ServiceAction(actionContext, action)
			}()
			return
		}
		if _, err := controller.ServiceAction(r.Context(), action); err != nil {
			http.Error(w, "service action failed", http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/operations?message=service-"+url.QueryEscape(action)+"-complete", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /operations/logs/{source}", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil {
			http.Error(w, "log source unavailable", http.StatusServiceUnavailable)
			return
		}
		file, err := controller.OpenLogSource(r.PathValue("source"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, r.PathValue("source")))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(w, file)
	})
	mux.HandleFunc("GET /operations/events", func(w http.ResponseWriter, r *http.Request) {
		controller := operationsController(data)
		if controller == nil {
			http.Error(w, "operations unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		disableStreamingDeadline(w)
		flusher, _ := w.(http.Flusher)
		subscription := controller.SubscribeLogs()
		defer subscription.Cancel()
		if snapshot, err := controller.Snapshot(r.Context()); err == nil {
			writeSSE(w, "service", snapshot.Service)
		}
		for _, record := range controller.RecentLogs(200) {
			writeSSE(w, "log", record)
		}
		if flusher != nil {
			flusher.Flush()
		}
		serviceTicker := time.NewTicker(5 * time.Second)
		defer serviceTicker.Stop()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case record, ok := <-subscription.C:
				if !ok {
					return
				}
				writeSSE(w, "log", record)
			case <-serviceTicker.C:
				if snapshot, err := controller.Snapshot(r.Context()); err == nil {
					writeSSE(w, "service", snapshot.Service)
				}
			case <-heartbeat.C:
				_, _ = io.WriteString(w, ": ping\n\n")
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/operations", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET /reference", func(w http.ResponseWriter, _ *http.Request) {
		catalog := defaultHelpCatalog()
		render(w, tmpl, nonce, pageData{Version: info, Page: "reference", Readiness: readiness(data), ReferenceTopics: catalog.All()})
	})
	return mux
}

func authoringSelection(values url.Values) authoring.Selection {
	return authoring.Selection{
		Section: values.Get("section"), Step: values.Get("step"),
		Pattern: values.Get("pattern"), Level: values.Get("level"), View: values.Get("view"),
	}
}

func authoringFields(fields []scenario.FieldDefinition, filter string) []scenario.FieldDefinition {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return fields
	}
	matched := make([]scenario.FieldDefinition, 0, len(fields))
	for _, field := range fields {
		searchable := strings.ToLower(strings.Join([]string{
			field.ID, field.Group, field.YAMLName, field.ValueType, string(field.Requirement),
			strings.Join(field.AllowedValues, " "), field.Limits, field.OmittedBehavior,
			field.PatternRestriction, field.Effect, field.Example, field.CommonError,
		}, " "))
		if strings.Contains(searchable, filter) {
			matched = append(matched, field)
		}
	}
	return matched
}

func scenarioWorkbench(ctx context.Context, info version.Info, data DataSource, imported []domain.ScenarioRecord, selected string) (pageData, error) {
	if selected == "" {
		selected = "builtin:flood"
	}
	view := pageData{Version: info, Page: "scenarios", Readiness: readiness(data), ImportedScenarios: imported, SelectedScenario: selected}
	for _, builtin := range scenario.AllBuiltins() {
		ref := "builtin:" + builtin.Pattern
		view.ScenarioCatalog = append(view.ScenarioCatalog, scenarioCatalogItem{Ref: ref, Name: builtin.Name, Pattern: builtin.Pattern, Kind: "Built-in", Selected: ref == selected})
	}
	for _, item := range imported {
		document, _ := scenario.Decode(strings.NewReader(item.SourceDocument))
		ref := "imported:" + item.ID
		view.ScenarioCatalog = append(view.ScenarioCatalog, scenarioCatalogItem{Ref: ref, Name: item.Name, Pattern: document.Pattern, Kind: "Custom", Selected: ref == selected})
	}
	var source []byte
	if pattern, ok := strings.CutPrefix(selected, "builtin:"); ok {
		var err error
		source, err = scenario.BuiltinSource(pattern)
		if err != nil {
			return pageData{}, err
		}
		view.SelectedBuiltIn = true
	} else if id, ok := strings.CutPrefix(selected, "imported:"); ok {
		for _, item := range imported {
			if item.ID == id {
				source = []byte(item.SourceDocument)
				break
			}
		}
		if len(source) == 0 {
			return pageData{}, fmt.Errorf("selected scenario is unavailable")
		}
		view.SelectedScenarioID = id
	} else if pattern, ok := strings.CutPrefix(selected, "draft:"); ok {
		document := scenario.Builtin(pattern)
		if len(document.Cases) != 2 {
			return pageData{}, fmt.Errorf("draft source is unavailable")
		}
		names := make(map[string]bool, len(imported))
		for _, item := range imported {
			names[item.Name] = true
		}
		baseName := document.Name + "-custom"
		document.Name = baseName
		for suffix := 2; names[document.Name]; suffix++ {
			document.Name = baseName + "-" + strconv.Itoa(suffix)
		}
		document.Suite = "custom"
		var err error
		source, err = scenario.Encode(document)
		if err != nil {
			return pageData{}, err
		}
		view.SelectedDraft = true
		view.ScenarioCatalog = append(view.ScenarioCatalog, scenarioCatalogItem{Ref: selected, Name: document.Name, Pattern: pattern, Kind: "Unsaved draft", Selected: true})
	} else {
		return pageData{}, fmt.Errorf("selected scenario reference is invalid")
	}
	document, err := scenario.Decode(bytes.NewReader(source))
	if err != nil {
		return pageData{}, err
	}
	view.SelectedName, view.SelectedPattern, view.ScenarioSource = document.Name, document.Pattern, string(source)
	if inspector, ok := data.(scenarioInspector); ok {
		inspection, inspectErr := inspector.InspectScenario(ctx, source, "phase_b_dispatch")
		if inspectErr != nil {
			view.Error = inspectErr.Error()
		} else {
			view.ScenarioPlan = &inspection.Plan
		}
	}
	return view, nil
}

func operationsController(data DataSource) *operations.Controller {
	provider, ok := data.(operationsProvider)
	if !ok {
		return nil
	}
	return provider.Operations()
}

func writeSSE(w io.Writer, event string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
}

func disableStreamingDeadline(w http.ResponseWriter) {
	// The server has a bounded WriteTimeout for ordinary responses. Streaming
	// endpoints own their lifetime through the request context and heartbeats.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

func detachedServiceActionContext(request *http.Request) (context.Context, context.CancelFunc) {
	// Stopping the service closes the request that initiated the stop. Preserve
	// request values while detaching cancellation, then keep the platform call
	// bounded so a failed service manager cannot leak a goroutine.
	return context.WithTimeout(context.WithoutCancel(request.Context()), 15*time.Second)
}

func render(w http.ResponseWriter, tmpl *template.Template, nonce nonceFunc, data pageData) {
	renderStatus(w, tmpl, nonce, http.StatusOK, data)
}

func renderStatus(w http.ResponseWriter, tmpl *template.Template, nonce nonceFunc, status int, data pageData) {
	helpID := data.Page
	if helpID == "settings" {
		helpID = "operations"
	}
	if topic, ok := defaultHelpCatalog().Topic(helpID); ok {
		data.Help = topic
	}
	n, err := nonce()
	if err != nil || n == "" {
		http.Error(w, "could not render page", http.StatusInternalServerError)
		return
	}
	data.Nonce = n
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "base", data); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
		return
	}
	setHTMLHeaders(w, n)
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

func setHTMLHeaders(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; script-src 'self' 'nonce-%s'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'", nonce))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
}

func readyStatus(data DataSource) (bool, string) {
	if data == nil {
		return true, ""
	}
	return data.ReadyStatus()
}

func readiness(data DataSource) readinessView {
	ready, message := readyStatus(data)
	return readinessView{Ready: ready, Error: message}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func csrfToken(w http.ResponseWriter, r *http.Request, secret []byte) string {
	cookie, err := r.Cookie("corrtest_csrf")
	if err != nil || cookie.Value == "" {
		value, randomErr := generateNonce()
		if randomErr != nil {
			return ""
		}
		// #nosec G124 -- Plan 2 is loopback-only HTTP; HttpOnly and Strict SameSite protect this non-authentication CSRF cookie.
		cookie = &http.Cookie{Name: "corrtest_csrf", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}
		http.SetCookie(w, cookie)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(cookie.Value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validCSRF(r *http.Request, secret []byte) bool {
	cookie, err := r.Cookie("corrtest_csrf")
	if err != nil || cookie.Value == "" {
		return false
	}
	provided := r.FormValue("csrf_token")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(cookie.Value))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(provided), []byte(want))
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return parsed.Scheme == scheme && parsed.Host == r.Host && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func loopbackHostGuard(next http.Handler, listenerAddress string) http.Handler {
	_, listenerPort, err := net.SplitHostPort(listenerAddress)
	if err != nil || listenerPort == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "listener host policy is unavailable", http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, splitErr := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if splitErr != nil || port != listenerPort || ip == nil || !ip.IsLoopback() {
			http.Error(w, "request host is not the loopback listener", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostGuardForListener(next http.Handler, listenerAddress string, security Security) http.Handler {
	if security.Mode != SecurityNone {
		return next
	}
	host, _, err := net.SplitHostPort(listenerAddress)
	if err != nil {
		return next
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return next
	}
	return loopbackHostGuard(next, listenerAddress)
}

func generateNonce() (string, error) {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// Run serves until the context is canceled or the server fails.
func Run(ctx context.Context, opts Options) error {
	if opts.ListenAddress == "" {
		return errListenAddressRequired
	}
	listener, err := net.Listen("tcp", opts.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	handler, err := NewHandlerWithOptions(opts.Version, opts.Data, opts.Security)
	if err != nil {
		_ = listener.Close()
		return err
	}
	handler = hostGuardForListener(handler, listener.Addr().String(), opts.Security)
	if opts.Logger != nil {
		handler = requestLogger(handler, opts.Logger)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if opts.TLSCertFile != "" || opts.TLSKeyFile != "" {
			errCh <- server.ServeTLS(listener, opts.TLSCertFile, opts.TLSKeyFile)
			return
		}
		errCh <- server.Serve(listener)
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

var requestSequence atomic.Uint64

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *loggingResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestLogger(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		} else {
			route = strings.TrimPrefix(route, r.Method+" ")
		}
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		logger.InfoContext(r.Context(), "http request",
			"request_id", strconv.FormatUint(requestSequence.Add(1), 36),
			"method", r.Method,
			"route", route,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"remote_ip", remote,
		)
	})
}

var _ http.Flusher = (*loggingResponseWriter)(nil)
var _ io.Writer = (*loggingResponseWriter)(nil)
