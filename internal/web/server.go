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
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
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
}

type pageData struct {
	Nonce     string
	Version   version.Info
	Page      string
	Targets   []domain.Target
	Runs      []domain.Run
	Run       *domain.Run
	Events    []domain.RunEvent
	Artifacts []domain.ArtifactEvidence
	Readiness readinessView
	CSRFToken string
	Error     string
	Status    string
	Verdict   string
	Cleanup   string
	Pattern   string
}

type readinessView struct {
	Ready bool
	Error string
}

type nonceFunc func() (string, error)

// NewHandler returns the Plan-1 shell with no durable data source.
func NewHandler(info version.Info) http.Handler {
	return newHandler(info, parsedTemplates, staticHandler, generateNonce)
}

// NewHandlerWithData returns the complete durable server-rendered application.
func NewHandlerWithData(info version.Info, data DataSource) http.Handler {
	return newHandlerWithData(info, data, parsedTemplates, staticHandler, generateNonce)
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
		render(w, tmpl, nonce, pageData{Version: info, Page: "run-detail", Run: &run, Events: events, Artifacts: artifacts, Readiness: readiness(data)})
	})
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, _ *http.Request) {
		render(w, tmpl, nonce, pageData{Version: info, Page: "settings", Readiness: readiness(data)})
	})
	return mux
}

func render(w http.ResponseWriter, tmpl *template.Template, nonce nonceFunc, data pageData) {
	renderStatus(w, tmpl, nonce, http.StatusOK, data)
}

func renderStatus(w http.ResponseWriter, tmpl *template.Template, nonce nonceFunc, status int, data pageData) {
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
	w.Header().Set("Referrer-Policy", "no-referrer")
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
	server := &http.Server{
		Handler: NewHandlerWithData(opts.Version, opts.Data), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
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
