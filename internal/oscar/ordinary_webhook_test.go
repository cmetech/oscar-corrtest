package oscar_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrdinaryWebhookRecordsActualRouteAndClearsOriginalNativeEpisode(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 123000000, time.UTC)
	start := now.Add(-time.Second)
	publicPosts := 0
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { publicPosts++; w.Write([]byte(`{"status":"accepted"}`)) }))
	defer public.Close()
	var saved oscar.InjectionRequestEvidence
	var bodies []map[string]any
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, _ := base64.StdEncoding.DecodeString(saved.BodyBase64)
		if string(raw) != string(body) {
			t.Error("receiver body differs from committed evidence")
		}
		if r.URL.Path != domain.OrdinaryWebhookPath || r.Header.Get("X-API-Key") != "receiver-key" || r.Header.Get("If-Match") != `"a823e17c4f3e3d33ffdebae28165b5835ab3255f37be8f0c6c745d8dabc92284"` {
			t.Error("receiver binding wrong")
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, value)
		w.Header().Set("X-Oscar-Profile-Revision", "a823e17c4f3e3d33ffdebae28165b5835ab3255f37be8f0c6c745d8dabc92284")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer receiver.Close()
	target := domain.Target{ID: "tgt_lab", BaseURL: public.URL, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "PUBLIC"}, OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "RECEIVER"}}}
	client, err := oscar.New(target, oscar.Options{InputPath: "ordinary-webhook", Now: func() time.Time { return now }, Getenv: func(key string) string {
		if key == "RECEIVER" {
			return "receiver-key"
		}
		return "public-key"
	}})
	if err != nil {
		t.Fatal(err)
	}
	alert := compiler.AlertPlan{Name: "A", Status: "firing", StartsAt: start, Labels: map[string]string{"alertname": "A", "oscar_test_event_id": "child-A", "oscar_test_run_id": "run", "oscar_test_alert_class": "source"}}
	result, err := client.InjectRecorded(context.Background(), alert, func(e oscar.InjectionRequestEvidence) error { saved = e; return nil })
	if err != nil || string(result.Class) != "forwarded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if saved.Endpoint != target.OrdinaryWebhook.Endpoint || saved.APIProfile != "corrtest_ordinary_v1" || publicPosts != 0 {
		t.Fatal("wrong recorded route")
	}
	result, err = client.ResolveHistoryRecorded(context.Background(), oscar.HistoryRecord{AlertName: "A", Fingerprint: "observed-fp", StartsAt: start, Labels: alert.Labels}, func(e oscar.InjectionRequestEvidence) error { saved = e; return nil })
	if err != nil || string(result.Class) != "forwarded" {
		t.Fatalf("clear=%+v err=%v", result, err)
	}
	if len(bodies) != 2 || publicPosts != 0 {
		t.Fatalf("receiver=%d public=%d", len(bodies), publicPosts)
	}
	for _, body := range bodies {
		if body["status"] != nil || body["alerts"] != nil || body["fingerPrint"] != nil || body["startsAt"] != start.Format(time.RFC3339Nano) {
			t.Fatal("wrong native profile body")
		}
		labels := body["labels"].(map[string]any)
		if labels["oscar_fingerprint"] != nil {
			t.Fatal("cleanup changed native identity")
		}
	}
	if bodies[0]["endsAt"] != start.Add(600*time.Second).Format(time.RFC3339Nano) || bodies[1]["endsAt"] != now.Format(time.RFC3339Nano) {
		t.Fatal("native endsAt not explicit")
	}
}

func TestOrdinaryWebhookUncertainForwardCannotBeOvertakenByClear(t *testing.T) {
	for _, mode := range []string{"no ack", "wrong ack", "acl denied", "send failed", "stale episode", "changed labels", "changed source class", "refire", "clear failed"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			start := now.Add(-time.Second)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "no ack" {
					w.Write([]byte(`{"status":"ok"}`))
					return
				}
				if mode == "wrong ack" {
					w.Header().Set("X-Oscar-Profile-Revision", "wrong")
					w.Write([]byte(`{"status":"ok"}`))
					return
				}
				w.Header().Set("X-Oscar-Profile-Revision", oscar.OrdinaryProfileHash)
				if mode == "acl denied" {
					w.Write([]byte(`{"status":"acl_denied"}`))
					return
				}
				if mode == "send failed" || (mode == "clear failed" && calls > 1) {
					w.WriteHeader(502)
					return
				}
				w.Write([]byte(`{"status":"ok"}`))
			}))
			defer server.Close()
			client, err := oscar.New(domain.Target{BaseURL: server.URL, APIProfile: "public-v1", OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: server.URL + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "KEY"}}}, oscar.Options{InputPath: "ordinary-webhook", Now: func() time.Time { return now }, Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			alert := compiler.AlertPlan{Name: "A", Status: "firing", StartsAt: start, Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "run", "oscar_test_event_id": "A", "oscar_test_alert_class": "source"}}
			recorder := func(oscar.InjectionRequestEvidence) error { return nil }
			result, sendErr := client.InjectRecorded(context.Background(), alert, recorder)
			uncertain := mode == "no ack" || mode == "wrong ack" || mode == "acl denied" || mode == "send failed"
			if uncertain && result.Class == oscar.InjectionForwarded {
				t.Fatal("uncertain response treated as pinned forwarding")
			}
			if !uncertain && (sendErr != nil || result.Class != oscar.InjectionForwarded) {
				t.Fatalf("forward=%+v err=%v", result, sendErr)
			}
			row := oscar.HistoryRecord{AlertName: "A", Fingerprint: "server-fp", StartsAt: start, Labels: map[string]string{}}
			for k, v := range alert.Labels {
				row.Labels[k] = v
			}
			if mode == "stale episode" {
				row.StartsAt = start.Add(-time.Second)
			}
			if mode == "changed source class" {
				row.Labels["oscar_test_alert_class"] = "synthetic"
			}
			if mode == "changed labels" {
				row.Labels["alertname"] = "other"
			}
			if mode == "refire" {
				alert.StartsAt = start.Add(time.Millisecond)
				if _, err := client.InjectRecorded(context.Background(), alert, recorder); err == nil {
					t.Fatal("unsupported refire sent")
				}
				if calls != 1 {
					t.Fatal("refire reached receiver")
				}
				return
			}
			if _, err := client.ResolveHistoryRecorded(context.Background(), row, recorder); err == nil {
				t.Fatal("unsafe or unknown clear reported success")
			}
			want := 1
			if mode == "clear failed" {
				want = 2
				if _, err := client.ResolveHistoryRecorded(context.Background(), row, recorder); err == nil {
					t.Fatal("unknown clear retried")
				}
			}
			if calls != want {
				t.Fatalf("requests=%d want=%d", calls, want)
			}
		})
	}
}

func TestOrdinaryProbeRequiresRecordedRouteAndNativeCleanup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	var native oscar.OrdinaryWebhookRequest
	receiverPosts := 0
	recorded := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receiverPosts++
		if err := json.NewDecoder(r.Body).Decode(&native); err != nil {
			t.Error(err)
		}
		w.Header().Set("X-Oscar-Profile-Revision", oscar.OrdinaryProfileHash)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer receiver.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/alerts/history" {
			t.Error("probe used public injection")
		}
		labels := []map[string]string{}
		for k, v := range native.Labels {
			labels = append(labels, map[string]string{"Label": k, "Value": v})
		}
		json.NewEncoder(w).Encode(map[string]any{"total_records": 1, "page": 1, "per_page": 100, "records": []map[string]any{{"id": "probe", "alertname": native.Labels["alertname"], "fingerprint": "server-probe", "startsAt": native.StartsAt, "status": "firing", "labels": labels}}})
	}))
	defer public.Close()
	client, err := oscar.New(domain.Target{BaseURL: public.URL, APIProfile: "public-v1", OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "KEY"}}}, oscar.Options{InputPath: "ordinary-webhook", Now: func() time.Time { return now }, Getenv: func(string) string { return "key" }})
	if err != nil {
		t.Fatal(err)
	}
	probeClient, ok := any(client).(interface {
		ProbeLabelSurvivalRecorded(context.Context, string, string, oscar.InjectionRecorder) (oscar.LabelProbeResult, error)
	})
	if !ok {
		t.Fatal("ordinary probe recorder unavailable")
	}
	probe, err := probeClient.ProbeLabelSurvivalRecorded(context.Background(), "crt_probe", "12345678", func(e oscar.InjectionRequestEvidence) error {
		recorded++
		if e.Endpoint != receiver.URL+domain.OrdinaryWebhookPath {
			t.Error("wrong probe evidence")
		}
		return nil
	})
	if err != nil || !probe.HistoryFound || !probe.Accepted || client.UnresolvedInputCleanup() || recorded != 2 || receiverPosts != 2 || native.EndsAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("probe=%+v err=%v records=%d posts=%d", probe, err, recorded, receiverPosts)
	}
}

func TestOrdinaryWebhookRecorderAndRedirectBoundary(t *testing.T) {
	for _, mode := range []string{"record failure", "redirect", "caller mutation", "clear no ack", "clear wrong ack"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			posts, redirected := 0, 0
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++; w.WriteHeader(200) }))
			defer sink.Close()
			var saved oscar.InjectionRequestEvidence
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				raw, _ := io.ReadAll(r.Body)
				expected, _ := base64.StdEncoding.DecodeString(saved.BodyBase64)
				if string(raw) != string(expected) {
					t.Error("wire differs from persisted frozen body")
				}
				var body oscar.OrdinaryWebhookRequest
				json.Unmarshal(raw, &body)
				if body.Labels["alertname"] != "A" {
					t.Error("caller mutation reached wire")
				}
				if mode == "redirect" {
					http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
					return
				}
				ack := oscar.OrdinaryProfileHash
				if posts == 2 && mode == "clear no ack" {
					ack = ""
				}
				if posts == 2 && mode == "clear wrong ack" {
					ack = "wrong"
				}
				w.Header().Set("X-Oscar-Profile-Revision", ack)
				w.Write([]byte(`{"status":"ok"}`))
			}))
			defer receiver.Close()
			client, err := oscar.New(domain.Target{BaseURL: receiver.URL, APIProfile: "public-v1", OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "KEY"}}}, oscar.Options{InputPath: "ordinary-webhook", Now: func() time.Time { return now }, Getenv: func(string) string { return "credential" }})
			if err != nil {
				t.Fatal(err)
			}
			alert := compiler.AlertPlan{Name: "A", Status: "firing", StartsAt: now.Add(-time.Second), Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "run", "oscar_test_event_id": "A", "oscar_test_alert_class": "source"}}
			result, err := client.InjectRecorded(context.Background(), alert, func(e oscar.InjectionRequestEvidence) error {
				saved = e
				if mode == "record failure" {
					return fmt.Errorf("disk unavailable")
				}
				if mode == "caller mutation" {
					alert.Labels["alertname"] = "changed"
				}
				return nil
			})
			if mode == "record failure" {
				if err == nil || posts != 0 || client.UnresolvedInputCleanup() {
					t.Fatalf("failed recording sent or retained possible input: %v %d", err, posts)
				}
				return
			}
			if mode == "redirect" {
				if result.Class == oscar.InjectionForwarded || redirected != 0 || posts != 1 || !client.UnresolvedInputCleanup() {
					t.Fatal("redirect followed or unknown input forgotten")
				}
				return
			}
			if err != nil || result.Class != oscar.InjectionForwarded {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			row := oscar.HistoryRecord{AlertName: "A", Fingerprint: "fp", StartsAt: alert.StartsAt, Labels: map[string]string{"alertname": "A", "oscar_test_run_id": "run", "oscar_test_event_id": "A", "oscar_test_alert_class": "source"}}
			clear, err := client.ResolveHistoryRecorded(context.Background(), row, func(e oscar.InjectionRequestEvidence) error { saved = e; return nil })
			if mode == "caller mutation" {
				if err != nil || clear.Class != oscar.InjectionForwarded || client.UnresolvedInputCleanup() {
					t.Fatalf("retained original changed: %+v %v", clear, err)
				}
				return
			}
			if clear.Class == oscar.InjectionForwarded || !client.UnresolvedInputCleanup() {
				t.Fatal("missing clear acknowledgment discharged cleanup")
			}
			if _, err := client.ResolveHistoryRecorded(context.Background(), row, func(e oscar.InjectionRequestEvidence) error { return nil }); err == nil || posts != 2 {
				t.Fatal("unknown clear automatically retried")
			}
		})
	}
}
