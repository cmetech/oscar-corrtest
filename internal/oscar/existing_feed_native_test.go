package oscar

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func nativeObserverTarget(origin string) domain.Target {
	return domain.Target{ID: "owned-native", BaseURL: origin, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OWNED_NATIVE_PASSWORD"}}
}

func TestExistingFeedNativeObserverUsesExactReadOnlyScopedTransport(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			manifest, err := FreezeExistingFeedManifest(existingFeedSpec(t, profile))
			if err != nil {
				t.Fatal(err)
			}
			evidence, _, _ := manifest.Prepare(0)
			native := ExistingFeedNativeObservation{Labels: evidence.Expected.Labels, StartsAt: evidence.Expected.StartsAt, Fingerprint: "0123456789abcdef"}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				username, password, ok := r.BasicAuth()
				if !ok || username != "publisher" || password != "owned-password" || r.Header.Get("X-API-Key") != "" {
					t.Error("wrong Native auth contract")
				}
				key := "node"
				if profile == "middleware_custom_alert" {
					key = "hostname"
				}
				if r.Method != "GET" || r.URL.Path != "/api/v2/alerts" || r.URL.Query().Get("filter") != key+`="`+evidence.Expected.Labels[key]+`"` || len(r.URL.Query()) != 1 || r.ContentLength != 0 {
					t.Errorf("unexpected Native read: %s %s length%d", r.Method, r.URL, r.ContentLength)
				}
				_ = json.NewEncoder(w).Encode([]ExistingFeedNativeObservation{native})
			}))
			defer server.Close()
			observer, err := NewExistingFeedNativeObserver(manifest, nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "owned-password" }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := observer.Observe(context.Background(), 0)
			if err != nil || !reflect.DeepEqual(got, native) {
				t.Fatalf("observation %+v %v", got, err)
			}
			if _, err := observer.Observe(context.Background(), 6); err == nil || calls.Load() != 1 {
				t.Fatal("invalid fixture performed request")
			}
		})
	}
}

func TestExistingFeedNativeObserverRefusesAmbiguousResponsesAndRedirects(t *testing.T) {
	for _, fault := range []string{"empty", "null", "duplicate", "foreign", "bad-json", "oversized", "redirect", "timeout"} {
		t.Run(fault, func(t *testing.T) {
			manifest, _ := FreezeExistingFeedManifest(existingFeedSpec(t, "splunk"))
			e, _, _ := manifest.Prepare(0)
			row := ExistingFeedNativeObservation{Labels: e.Expected.Labels, StartsAt: e.Expected.StartsAt, Fingerprint: "0123456789abcdef"}
			var redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch fault {
				case "empty":
					_, _ = w.Write([]byte(`[]`))
				case "null":
					_, _ = w.Write([]byte(`null`))
				case "duplicate":
					_ = json.NewEncoder(w).Encode([]ExistingFeedNativeObservation{row, row})
				case "foreign":
					row.Labels["site"] = "foreign"
					_ = json.NewEncoder(w).Encode([]ExistingFeedNativeObservation{row})
				case "bad-json":
					_, _ = w.Write([]byte(`{`))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
				case "redirect":
					http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			observer, err := NewExistingFeedNativeObserver(manifest, nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "owned-password" }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err = observer.Observe(ctx, 0); err == nil {
				t.Fatal("ambiguous Native response accepted")
			}
			if errors.Is(err, ErrExistingFeedNativeNotObserved) != (fault == "empty") {
				t.Fatalf("only an empty array permits observation polling: %v", err)
			}
			if redirected.Load() != 0 {
				t.Fatal("redirect exposed credential")
			}
		})
	}
}

func TestExistingFeedNativeObserverRejectsUnsafeBinding(t *testing.T) {
	manifest, _ := FreezeExistingFeedManifest(existingFeedSpec(t, "splunk"))
	for _, fault := range []string{"missing-id", "missing-credential", "empty-credential", "query", "userinfo", "path", "custom-client", "input-route", "ordinary-binding"} {
		t.Run(fault, func(t *testing.T) {
			target := nativeObserverTarget("https://owned-native:9093")
			options := Options{Getenv: func(string) string { return "owned-password" }}
			switch fault {
			case "missing-id":
				target.ID = ""
			case "missing-credential":
				target.Credential = domain.CredentialRef{}
			case "empty-credential":
				options.Getenv = func(string) string { return "" }
			case "query":
				target.BaseURL += "?token=secret"
			case "userinfo":
				target.BaseURL = "https://u:p@owned-native"
			case "path":
				target.BaseURL += "/other"
			case "custom-client":
				options.HTTPClient = &http.Client{}
			case "input-route":
				options.InputPath = "alert-engine"
			case "ordinary-binding":
				target.OrdinaryWebhook = &domain.OrdinaryWebhookBinding{}
			}
			if _, err := NewExistingFeedNativeObserver(manifest, target, options); err == nil {
				t.Fatal("unsafe Native binding accepted")
			}
		})
	}
}

func TestExistingFeedNativeObserverPreservesTLSVerification(t *testing.T) {
	manifest, _ := FreezeExistingFeedManifest(existingFeedSpec(t, "splunk"))
	evidence, _, _ := manifest.Prepare(0)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != "publisher" || password != "owned-password" {
			t.Error("missing TLS Native auth")
		}
		_ = json.NewEncoder(w).Encode([]ExistingFeedNativeObservation{{Labels: evidence.Expected.Labels, StartsAt: evidence.Expected.StartsAt, Fingerprint: "0123456789abcdef"}})
	}))
	defer server.Close()
	target := nativeObserverTarget(server.URL)
	options := Options{Getenv: func(string) string { return "owned-password" }}
	observer, err := NewExistingFeedNativeObserver(manifest, target, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = observer.Observe(context.Background(), 0); err == nil || requests.Load() != 0 {
		t.Fatal("untrusted TLS accepted")
	}
	caPath := filepath.Join(t.TempDir(), "owned-ca.pem")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	target.TLS.CAPath = caPath
	observer, err = NewExistingFeedNativeObserver(manifest, target, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = observer.Observe(context.Background(), 0); err != nil || requests.Load() != 1 {
		t.Fatalf("trusted TLS rejected: %v", err)
	}
}
