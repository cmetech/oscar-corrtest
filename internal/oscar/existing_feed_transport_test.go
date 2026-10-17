package oscar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type existingFeedRoundTripper func(*http.Request) (*http.Response, error)

func (f existingFeedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func existingFeedReceiver(t *testing.T, profile, origin string) (ExistingFeedManifest, domain.Target, Options) {
	t.Helper()
	spec := existingFeedSpec(t, profile)
	spec.ReceiverOrigin = origin
	manifest, err := FreezeExistingFeedManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, domain.Target{ID: spec.TargetID, BaseURL: origin, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "RECEIVER_KEY"}}, Options{Getenv: func(name string) string {
		if name == "RECEIVER_KEY" {
			return "receiver-secret"
		}
		return "api-secret"
	}}
}

func TestExistingFeedTransportRecordsBeforeExactSinglePOST(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			var recorded ExistingFeedRequestEvidence
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				frozen, err := base64.StdEncoding.DecodeString(recorded.Request.BodyBase64)
				if err != nil || string(body) != string(frozen) || recorded.Manifest.Mapping.BodyBase64 == "" {
					t.Error("complete evidence not recorded before exact send")
				}
				if r.Method != "POST" || r.URL.Path != "/api/v1/webhookreceiver/webhook/"+profile || r.Host != "owned-receiver" || r.UserAgent() != "owned-existing-feed/1" || r.Header.Get("If-Match") != `"`+recorded.Manifest.ProfileHash+`"` || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-API-Key") != "receiver-secret" {
					t.Errorf("wrong request: %+v", r)
				}
				w.Header().Set("X-Oscar-Profile-Revision", recorded.Manifest.ProfileHash)
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			defer server.Close()
			manifest, target, options := existingFeedReceiver(t, profile, server.URL)
			input, err := NewExistingFeedLabInput(manifest, target, options)
			if err != nil {
				t.Fatal(err)
			}
			underlying := input.transport.http.Transport
			input.transport.http.Transport = existingFeedRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.GetBody != nil || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
					t.Error("prepared POST is replayable")
				}
				return underlying.RoundTrip(r)
			})
			records := 0
			result, err := input.SendRecorded(context.Background(), 0, func(e ExistingFeedRequestEvidence) error {
				records++
				recorded = e
				encoded, _ := json.Marshal(e)
				if strings.Contains(string(encoded), "receiver-secret") || strings.Contains(string(encoded), "api-secret") {
					t.Error("credentials leaked into evidence")
				}
				e.Expected.Labels["alertname"] = "caller mutation"
				return nil
			})
			if err != nil || result.Class != InjectionForwarded || calls.Load() != 1 || records != 1 {
				t.Fatalf("result=%+v err=%v calls=%d records=%d", result, err, calls.Load(), records)
			}
			if _, err := input.SendRecorded(context.Background(), 0, func(ExistingFeedRequestEvidence) error { records++; return nil }); err == nil || records != 1 || calls.Load() != 1 {
				t.Fatal("repeated prepared fixture resent")
			}
		})
	}
}

func TestExistingFeedTransportRefusesBeforeSend(t *testing.T) {
	for _, mode := range []string{"missing recorder", "recorder failure", "canceled before record", "canceled after record", "foreign index"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer server.Close()
			manifest, target, options := existingFeedReceiver(t, "splunk", server.URL)
			input, err := NewExistingFeedLabInput(manifest, target, options)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			records, index := 0, 0
			var record ExistingFeedRecorder = func(ExistingFeedRequestEvidence) error {
				records++
				if mode == "canceled after record" {
					cancel()
					return nil
				}
				return errors.New("disk full")
			}
			if mode == "missing recorder" {
				record = nil
			}
			if mode == "canceled before record" {
				cancel()
			}
			if mode == "foreign index" {
				index = 6
			}
			_, err = input.SendRecorded(ctx, index, record)
			wantRecords := 0
			if mode == "recorder failure" || mode == "canceled after record" {
				wantRecords = 1
			}
			if err == nil || calls.Load() != 0 || records != wantRecords {
				t.Fatalf("err=%v calls=%d records=%d", err, calls.Load(), records)
			}
			if mode == "canceled after record" {
				if _, err := input.SendRecorded(context.Background(), 0, func(ExistingFeedRequestEvidence) error { records++; return nil }); err == nil || records != 1 || calls.Load() != 0 {
					t.Fatal("committed attempt repeated after cancellation")
				}
			}
		})
	}
}

func TestExistingFeedTransportAmbiguityNeverRetries(t *testing.T) {
	for _, mode := range []string{"missing ack", "wrong ack", "multiple ack", "malformed body", "redirect 302", "redirect 307", "redirect 308", "timeout", "connection lost", "oversize response", "acl denied", "precondition failure", "wrong success status"} {
		t.Run(mode, func(t *testing.T) {
			var calls, redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer destination.Close()
			spec := existingFeedSpec(t, "splunk")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Oscar-Profile-Revision", spec.ProfileHash)
				switch mode {
				case "missing ack":
					w.Header().Del("X-Oscar-Profile-Revision")
				case "wrong ack":
					w.Header().Set("X-Oscar-Profile-Revision", strings.Repeat("0", 64))
				case "multiple ack":
					w.Header().Add("X-Oscar-Profile-Revision", strings.Repeat("0", 64))
				case "malformed body":
					_, _ = io.WriteString(w, `{`)
					return
				case "redirect 302":
					http.Redirect(w, r, destination.URL, 302)
					return
				case "redirect 307":
					http.Redirect(w, r, destination.URL, 307)
					return
				case "redirect 308":
					http.Redirect(w, r, destination.URL, 308)
					return
				case "timeout":
					_, _ = io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
					return
				case "connection lost":
					_, _ = io.Copy(io.Discard, r.Body)
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				case "oversize response":
					_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
					return
				case "acl denied":
					_, _ = io.WriteString(w, `{"status":"acl_denied"}`)
					return
				case "precondition failure":
					w.WriteHeader(412)
					return
				case "wrong success status":
					w.WriteHeader(202)
				}
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			defer server.Close()
			manifest, target, options := existingFeedReceiver(t, "splunk", server.URL)
			input, err := NewExistingFeedLabInput(manifest, target, options)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			}
			records := 0
			result, err := input.SendRecorded(ctx, 0, func(ExistingFeedRequestEvidence) error { records++; return nil })
			want := InjectionIndeterminate
			if mode == "acl denied" {
				want = InjectionRejected
			}
			if result.Class != want || records != 1 || calls.Load() != 1 || redirected.Load() != 0 {
				t.Fatalf("result=%+v err=%v records=%d calls=%d redirected=%d", result, err, records, calls.Load(), redirected.Load())
			}
			if _, err := input.SendRecorded(context.Background(), 0, func(ExistingFeedRequestEvidence) error { records++; return nil }); err == nil || records != 1 || calls.Load() != 1 {
				t.Fatal("ambiguous/rejected input resent")
			}
		})
	}
}

func TestExistingFeedTransportRequiresBoundReceiver(t *testing.T) {
	for _, mode := range []string{"wrong target", "wrong origin", "missing credential", "empty credential", "ordinary binding", "custom transport", "wrong input route", "TLS conflict", "zero manifest", "wrong API profile"} {
		t.Run(mode, func(t *testing.T) {
			manifest, target, options := existingFeedReceiver(t, "splunk", "http://owned-receiver")
			switch mode {
			case "wrong target":
				target.ID = "foreign"
			case "wrong origin":
				target.BaseURL = "https://foreign"
			case "missing credential":
				target.Credential = domain.CredentialRef{}
			case "empty credential":
				options.Getenv = func(string) string { return "   " }
			case "ordinary binding":
				target.OrdinaryWebhook = &domain.OrdinaryWebhookBinding{}
			case "custom transport":
				options.HTTPClient = &http.Client{}
			case "wrong input route":
				options.InputPath = "ordinary-webhook"
			case "TLS conflict":
				target.TLS = domain.TLSPolicy{Insecure: true, CAPath: "/tmp/cert.pem"}
			case "zero manifest":
				manifest = ExistingFeedManifest{}
			case "wrong API profile":
				target.APIProfile = "splunk"
			}
			if _, err := NewExistingFeedLabInput(manifest, target, options); err == nil {
				t.Fatal("accepted unbound receiver")
			}
		})
	}
}

func TestExistingFeedTransportUsesConfiguredCA(t *testing.T) {
	var calls atomic.Int32
	spec := existingFeedSpec(t, "middleware_custom_alert")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "owned-receiver" || r.Header.Get("X-API-Key") != "receiver-secret" {
			t.Error("TLS request lost prepared host/credential")
		}
		w.Header().Set("X-Oscar-Profile-Revision", spec.ProfileHash)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "receiver-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, target, options := existingFeedReceiver(t, "middleware_custom_alert", server.URL)
	target.TLS.CAPath = path
	input, err := NewExistingFeedLabInput(manifest, target, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := input.SendRecorded(context.Background(), 0, func(ExistingFeedRequestEvidence) error { return nil })
	if err != nil || result.Class != InjectionForwarded || calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestExistingFeedTransportConcurrentAttemptIsNotRepeated(t *testing.T) {
	var calls, records atomic.Int32
	spec := existingFeedSpec(t, "splunk")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Oscar-Profile-Revision", spec.ProfileHash)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	manifest, target, options := existingFeedReceiver(t, "splunk", server.URL)
	input, err := NewExistingFeedLabInput(manifest, target, options)
	if err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := input.SendRecorded(context.Background(), 0, func(ExistingFeedRequestEvidence) error { records.Add(1); return nil })
			errors <- err
		}()
	}
	first, second := <-errors, <-errors
	if (first == nil) == (second == nil) || calls.Load() != 1 || records.Load() != 1 {
		t.Fatalf("errors=%v/%v calls=%d records=%d", first, second, calls.Load(), records.Load())
	}
}
