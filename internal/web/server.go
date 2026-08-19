package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

var errListenAddressRequired = errors.New("listen address is required")

// Options configures the embedded HTTP server.
type Options struct {
	ListenAddress string
	Version       version.Info
}

type pageData struct {
	Nonce   string
	Version version.Info
}

type nonceFunc func() (string, error)

// NewHandler returns the complete embedded web application.
func NewHandler(info version.Info) http.Handler {
	return newHandler(info, parsedTemplates, staticHandler, generateNonce)
}

func newHandler(info version.Info, tmpl *template.Template, static http.Handler, nonce nonceFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCache(static)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		n, err := nonce()
		if err != nil || n == "" {
			http.Error(w, "could not render page", http.StatusInternalServerError)
			return
		}

		var body bytes.Buffer
		if err := tmpl.ExecuteTemplate(&body, "base", pageData{Nonce: n, Version: info}); err != nil {
			http.Error(w, "could not render page", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; script-src 'self' 'nonce-%s'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'", n))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body.Bytes())
	})
	return mux
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
		Handler:           NewHandler(opts.Version),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
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
