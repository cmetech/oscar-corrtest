package oscar

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// This topology models stock rewriting and public credential checks. It is not
// proof that a deployed Traefik/Middleware instance enforced its RBAC policy.
func installedPublicProxy(t *testing.T, prefix, key string, backend http.Handler) (*httptest.Server, domain.Target, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	internal := httptest.NewServer(backend)
	t.Cleanup(internal.Close)
	destination, _ := url.Parse(internal.URL)
	proxy := httputil.NewSingleHostReverseProxy(destination)
	original := proxy.Director
	proxy.Director = func(r *http.Request) {
		original(r)
		if prefix == "/ext/mw" {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			r.Header.Set("X-Forwarded-Prefix", "/ext/mw")
		} else if prefix == "/webhookreceiver" {
			r.URL.Path = "/api/v1/webhookreceiver" + strings.TrimPrefix(r.URL.Path, prefix)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-API-Key") != key || !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.Error(w, "public authentication/path refused", 401)
			return
		}
		for _, name := range []string{"X-Forwarded-Prefix", "X-Internal-Service", "X-User-Id", "Cookie"} {
			if r.Header.Get(name) != "" {
				t.Errorf("client supplied authority %s", name)
				http.Error(w, "spoof", 403)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	target := nativeObserverTarget(server.URL + prefix)
	target.TLS.CAPath = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(target.TLS.CAPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return server, target, calls
}

func TestInstalledPublicReadProxyTopology(t *testing.T) {
	for _, prefix := range []string{"", "/ext/mw"} {
		for _, kind := range []string{"preflight", "audit", "active"} {
			t.Run(prefix+"/"+kind, func(t *testing.T) {
				expected, binding, rows := installedPreflightFixture(t)
				count := 0
				paths := []string{"/api/v1/packs", installedPackPrefix + "/configuration/loads/" + binding.LoadID, installedPackPrefix + "/configuration/items/" + expected.MappingRowKey, installedRulesPath, installedRulesPath + "/7", installedRulesPath + "/runtime"}
				_, target, publicCalls := installedPublicProxy(t, prefix, "public-key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-API-Key") != "public-key" || r.Header.Get("X-Forwarded-Prefix") != prefix {
						t.Error("lost proxy-set authentication context")
					}
					if r.Method != "GET" {
						t.Error("unexpected mutation")
					}
					switch kind {
					case "preflight":
						if count >= len(paths) || r.URL.Path != paths[count] {
							t.Errorf("wrong backend path %s", r.URL.Path)
							w.WriteHeader(500)
							return
						}
						_ = json.NewEncoder(w).Encode(rows[count])
					case "audit":
						if r.URL.Path != correlationAuditPath {
							t.Error("wrong backend audit path")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"rows": []any{}, "total": 0, "page": 1, "perPage": 100})
					case "active":
						if r.URL.Path != installedActivePath {
							t.Error("wrong backend Active path")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"records": []any{}, "total_records": 0, "total_pages": 0})
					}
					count++
				}))
				options := Options{Getenv: func(string) string { return "public-key" }}
				var err error
				switch kind {
				case "preflight":
					var reader *InstalledPackReader
					reader, err = NewInstalledPackReader(target, options)
					if err == nil {
						binding.TargetOrigin = target.BaseURL
						_, err = reader.Read(context.Background(), expected, binding)
					}
				case "audit":
					var reader *InstalledPackAuditReader
					reader, err = NewInstalledPackAuditReader(target, options)
					if err == nil {
						_, err = reader.FindAudit(context.Background(), "source-fingerprint", installedAuditRule())
					}
				case "active":
					var reader *InstalledPackActiveReader
					reader, err = NewInstalledPackActiveReader(target, options)
					if err == nil {
						_, err = reader.FindActive(context.Background(), "run-1")
					}
				}
				want := 1
				if kind == "preflight" {
					want = 6
				}
				if err != nil || count != want || int(publicCalls.Load()) != want {
					t.Fatalf("calls=%d/%d err=%v", count, publicCalls.Load(), err)
				}
			})
		}
	}
}

func TestInstalledPublicReceiverProxyTopology(t *testing.T) {
	for _, prefix := range []string{"", "/webhookreceiver"} {
		t.Run(prefix, func(t *testing.T) {
			var retained InstalledPackInputEvidence
			count := 0
			server, target, calls := installedPublicProxy(t, prefix, "receiver-secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				raw, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/api/v1/webhookreceiver/webhook/corrtest_operational_v1" || r.Header.Get("X-API-Key") != "receiver-secret" || r.Header.Get("If-Match") != retained.Request.ProfilePrecondition {
					t.Error("receiver rewrite/auth/precondition mismatch")
				}
				if len(raw) != retained.Request.BodyBytes || r.Header.Get("User-Agent") != "oscar-corrtest/installed-pack-v1" {
					t.Error("receiver body/UA mismatch")
				}
				w.Header().Set("X-Oscar-Profile-Revision", strings.Trim(retained.Request.ProfilePrecondition, `"`))
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			kit, _, options := installedInputFixture(t, target.BaseURL)
			input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), options.Now().Add(-time.Second), target, options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := input.SendRecorded(context.Background(), 0, func(e InstalledPackInputEvidence) error { retained = e; return nil })
			suffix := "/api/v1/webhookreceiver/webhook/corrtest_operational_v1"
			if prefix != "" {
				suffix = "/webhookreceiver/webhook/corrtest_operational_v1"
			}
			if err != nil || result.Class != InjectionForwarded || count != 1 || calls.Load() != 1 || retained.Request.Endpoint != server.URL+suffix {
				t.Fatalf("result=%+v evidence=%+v err=%v", result, retained.Request, err)
			}
		})
	}
}

func TestInstalledPublicBaseAliasesRefused(t *testing.T) {
	suffixes := []string{"/ext/mw/", "/ext/mw/extra", "/%65xt/mw", "/ext%2fmw", "/ext//mw", "/ext/mw/../mw", "//ext/mw", "?", "#", "/ext/mw?", "/ext/mw#", "/ext/mw\\other", "/webhookreceiver"}
	for _, suffix := range suffixes {
		target := nativeObserverTarget("https://target.test" + suffix)
		options := Options{Getenv: func(string) string { return "key" }}
		if _, err := NewInstalledPackReader(target, options); err == nil {
			t.Errorf("preflight accepted %q", suffix)
		}
		if _, err := NewInstalledPackAuditReader(target, options); err == nil {
			t.Errorf("audit accepted %q", suffix)
		}
		if _, err := NewInstalledPackActiveReader(target, options); err == nil {
			t.Errorf("active accepted %q", suffix)
		}
	}
	for _, suffix := range []string{"/webhookreceiver/", "/webhookreceiver/extra", "/%77ebhookreceiver", "/webhookreceiver/../webhookreceiver", "/webhookreceiver?", "/webhookreceiver#", "/ext/mw"} {
		kit, target, options := installedInputFixture(t, "https://receiver.test"+suffix)
		if _, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), options.Now().Add(-time.Second), target, options); err == nil {
			t.Errorf("receiver accepted %q", suffix)
		}
	}
}

func TestInstalledPublicTransportCannotDropOrEscapePrefix(t *testing.T) {
	for _, kind := range []string{"preflight", "audit", "active"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			defer server.Close()
			target := nativeObserverTarget(server.URL + "/ext/mw")
			options := Options{Getenv: func(string) string { return "key" }}
			var client *Client
			var err error
			path := ""
			switch kind {
			case "preflight":
				var reader *InstalledPackReader
				reader, err = NewInstalledPackReader(target, options)
				if err == nil {
					client = reader.client
				}
				path = "/api/v1/packs"
			case "audit":
				var reader *InstalledPackAuditReader
				reader, err = NewInstalledPackAuditReader(target, options)
				if err == nil {
					client = reader.client
				}
				path = correlationAuditPath
			case "active":
				var reader *InstalledPackActiveReader
				reader, err = NewInstalledPackActiveReader(target, options)
				if err == nil {
					client = reader.client
				}
				path = installedActivePath
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []string{server.URL + path, server.URL + "/ext/mw-other" + path, server.URL + "/ext/mw/ext/mw" + path, server.URL + "/%65xt/mw" + path, "https://foreign.test/ext/mw" + path} {
				request, _ := http.NewRequest("GET", endpoint, nil)
				if _, err := client.http.Transport.RoundTrip(request); err == nil {
					t.Errorf("accepted escape %s", endpoint)
				}
			}
			if calls != 0 {
				t.Fatalf("invalid request reached network %d", calls)
			}
		})
	}
}

func TestInstalledPublicNeverFollowsSameOriginRedirect(t *testing.T) {
	for _, kind := range []string{"preflight", "audit", "active", "input"} {
		for _, status := range []int{301, 302, 307, 308} {
			t.Run(kind+"/"+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Location", "/api/v1/packs")
					w.WriteHeader(status)
				}))
				defer server.Close()
				target := nativeObserverTarget(server.URL + "/ext/mw")
				options := Options{Getenv: func(string) string { return "key" }}
				var err error
				switch kind {
				case "preflight":
					e, b, _ := installedPreflightFixture(t)
					b.TargetOrigin = target.BaseURL
					r, createErr := NewInstalledPackReader(target, options)
					if createErr != nil {
						t.Fatal(createErr)
					}
					var observed *InstalledPackObservation
					observed, err = r.Read(context.Background(), e, b)
					if observed != nil {
						t.Fatal("redirect produced observation")
					}
				case "audit":
					r, createErr := NewInstalledPackAuditReader(target, options)
					if createErr != nil {
						t.Fatal(createErr)
					}
					var observed []InstalledPackAuditRecord
					observed, err = r.FindAudit(context.Background(), "source-fingerprint", installedAuditRule())
					if observed != nil {
						t.Fatal("redirect produced audit")
					}
				case "active":
					r, createErr := NewInstalledPackActiveReader(target, options)
					if createErr != nil {
						t.Fatal(createErr)
					}
					var observed []InstalledPackActiveRecord
					observed, err = r.FindActive(context.Background(), "run-1")
					if observed != nil {
						t.Fatal("redirect produced Active")
					}
				case "input":
					kit, receiver, opts := installedInputFixture(t, server.URL+"/webhookreceiver")
					r, createErr := NewInstalledPackInput(kit, strings.Repeat("a", 32), opts.Now().Add(-time.Second), receiver, opts)
					if createErr != nil {
						t.Fatal(createErr)
					}
					recorded := 0
					var result InstalledPackInputResult
					result, err = r.SendRecorded(context.Background(), 0, func(InstalledPackInputEvidence) error { recorded++; return nil })
					if recorded != 1 || result.Class != InjectionIndeterminate {
						t.Fatal("redirect lost unknown responsibility")
					}
					if _, retryErr := r.SendRecorded(context.Background(), 0, func(InstalledPackInputEvidence) error { recorded++; return nil }); retryErr == nil || recorded != 1 {
						t.Fatal("retried uncertain redirect")
					}
				}
				if err == nil || calls.Load() != 1 {
					t.Fatalf("redirect followed/accepted calls=%d err=%v", calls.Load(), err)
				}
			})
		}
	}
}

func TestInstalledPublicOriginalBindingCannotLosePrefix(t *testing.T) {
	expected, binding, rows := installedPreflightFixture(t)
	_, target, calls := installedPublicProxy(t, "/ext/mw", "key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(rows[0]) }))
	reader, err := NewInstalledPackReader(target, Options{Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	binding.TargetOrigin = strings.TrimSuffix(target.BaseURL, "/ext/mw")
	if observed, err := reader.Read(context.Background(), expected, binding); err == nil || observed != nil || calls.Load() != 0 {
		t.Fatal("adopted same-origin root installation")
	}
}

func TestInstalledPublicProxyDenialCannotFallbackToInternal(t *testing.T) {
	backendCalls := atomic.Int32{}
	_, target, calls := installedPublicProxy(t, "/ext/mw", "correct-key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backendCalls.Add(1) }))
	reader, err := NewInstalledPackActiveReader(target, Options{Getenv: func(string) string { return "wrong-key" }})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := reader.FindActive(context.Background(), "run-1"); err == nil || rows != nil || calls.Load() != 1 || backendCalls.Load() != 0 {
		t.Fatal("public denial bypassed or accepted")
	}
}
