package oscar_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

type recordedInjector interface {
	InjectRecorded(context.Context, compiler.AlertPlan, oscar.InjectionRecorder) (oscar.InjectionResult, error)
}

func recorded(t *testing.T, c *oscar.Client) recordedInjector {
	t.Helper()
	value, ok := any(c).(recordedInjector)
	if !ok {
		t.Fatal("client lacks required recorded before-send injection")
	}
	return value
}
func TestRecordedInjectionSendsFrozenBodyAfterRecording(t *testing.T) {
	for _, status := range []string{"firing", "resolved", "legacy"} {
		t.Run(status, func(t *testing.T) {
			var evidence oscar.InjectionRequestEvidence
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				saved, err := base64.StdEncoding.DecodeString(evidence.BodyBase64)
				if err != nil || !bytes.Equal(raw, saved) {
					t.Errorf("wire differed from recorded bytes: %v", err)
				}
				sum := sha256.Sum256(raw)
				if evidence.BodySHA256 != hex.EncodeToString(sum[:]) || evidence.BodyBytes != len(raw) || evidence.Endpoint != "http://"+r.Host+r.URL.Path || evidence.TargetID != "target-test" || evidence.Method != "POST" || evidence.APIProfile != "public-v1" || evidence.ContentType != "application/json" {
					t.Error("wrong prepared request binding")
				}
				if r.Header.Get("X-API-Key") != "credential-sentinel" {
					t.Error("credential not used for actual send")
				}
				if bytes.Contains(raw, []byte("mutated")) {
					t.Error("caller changed frozen bytes")
				}
				var payload oscar.AlertGroupRequest
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Error(err)
				}
				alert := payload.Alerts[0]
				if status == "legacy" {
					if alert.StartsAt != "" || alert.EndsAt != "" {
						t.Error("legacy timestamps were invented")
					}
				} else if alert.StartsAt != "2026-10-13T12:00:00.123Z" || (status == "resolved" && alert.EndsAt != "2026-10-13T12:00:03.123Z") {
					t.Error("prepared timestamps changed")
				}
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			}))
			defer server.Close()
			client, err := oscar.New(domain.Target{ID: "target-test", BaseURL: server.URL, APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "credential-sentinel" }})
			if err != nil {
				t.Fatal(err)
			}
			alert := compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A", "site": "NYC"}, Annotations: map[string]string{"summary": "original"}}
			if status != "legacy" {
				alert.StartsAt = time.Date(2026, 10, 13, 12, 0, 0, 123000000, time.UTC)
			}
			if status == "resolved" {
				alert.Status = status
				alert.EndsAt = alert.StartsAt.Add(3 * time.Second)
			}
			result, err := recorded(t, client).InjectRecorded(context.Background(), alert, func(value oscar.InjectionRequestEvidence) error {
				evidence = value
				encoded, _ := json.Marshal(value)
				if strings.Contains(string(encoded), "credential-sentinel") || strings.Contains(string(encoded), "X-API-Key") {
					t.Error("credential entered evidence")
				}
				alert.Labels["site"] = "mutated"
				alert.Annotations["summary"] = "mutated"
				return nil
			})
			if err != nil || result.Class != oscar.InjectionAccepted || calls != 1 {
				t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
			}
		})
	}
}
func TestRecordedInjectionRefusesUncommittedOrUnsafeRequest(t *testing.T) {
	for _, mode := range []string{"callback error", "missing callback", "userinfo", "query", "fragment", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			calls, records := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
			defer server.Close()
			endpoint := server.URL
			switch mode {
			case "userinfo":
				endpoint = strings.Replace(endpoint, "http://", "http://user:credential-sentinel@", 1)
			case "query":
				endpoint += "?key=credential-sentinel"
			case "fragment":
				endpoint += "#credential-sentinel"
			}
			client, err := oscar.New(domain.Target{BaseURL: endpoint, APIProfile: "public-v1"}, oscar.Options{})
			if err != nil {
				t.Fatal(err)
			}
			alert := compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A"}}
			if mode == "oversize" {
				alert.Labels["large"] = strings.Repeat("x", 1<<20)
			}
			var callback oscar.InjectionRecorder = func(oscar.InjectionRequestEvidence) error { records++; return errors.New("record failed") }
			if mode == "missing callback" {
				callback = nil
			}
			_, err = recorded(t, client).InjectRecorded(context.Background(), alert, callback)
			wantRecords := 0
			if mode == "callback error" {
				wantRecords = 1
			}
			if err == nil || calls != 0 || records != wantRecords {
				t.Fatalf("calls=%d records=%d err=%v", calls, records, err)
			}
			if strings.Contains(err.Error(), "credential-sentinel") {
				t.Fatal("unsafe URL leaked via error")
			}
		})
	}
}
func TestRecordedInjectionDoesNotFollowRedirectOrAlterOtherCalls(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			redirected := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected++
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			}))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, status) }))
			defer server.Close()
			transport := &http.Client{}
			client, err := oscar.New(domain.Target{BaseURL: server.URL, APIProfile: "public-v1"}, oscar.Options{HTTPClient: transport})
			if err != nil {
				t.Fatal(err)
			}
			alert := compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A"}}
			records := 0
			_, err = recorded(t, client).InjectRecorded(context.Background(), alert, func(oscar.InjectionRequestEvidence) error { records++; return nil })
			if err == nil || redirected != 0 || records != 1 {
				t.Fatalf("recorded request redirected: count=%d records=%d err=%v", redirected, records, err)
			}
			if _, err := client.Inject(context.Background(), alert); err != nil || redirected != 1 {
				t.Fatalf("legacy redirect policy changed: count=%d err=%v", redirected, err)
			}
			if transport.CheckRedirect != nil {
				t.Fatal("shared HTTP client mutated")
			}
		})
	}
}

func TestRecordedInjectionCancellationAfterCommitPreventsPOST(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client, err := oscar.New(domain.Target{BaseURL: server.URL + "/nested/", APIProfile: "public-v1"}, oscar.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	records := 0
	_, err = client.InjectRecorded(ctx, compiler.AlertPlan{Name: "A", Status: "firing", Labels: map[string]string{"alertname": "A"}}, func(e oscar.InjectionRequestEvidence) error {
		records++
		if e.Endpoint != server.URL+"/nested/api/v1/alerts" {
			t.Error("wrong nested endpoint")
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || records != 1 || calls != 0 {
		t.Fatalf("records=%d calls=%d err=%v", records, calls, err)
	}
}
