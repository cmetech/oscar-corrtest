package oscar_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestOrdinaryProbeUsesObservedHistorySeverityWithoutChangingNativeLabels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		top   any
		label string
		pass  bool
	}{
		{"promoted severity", "warning", "", true},
		{"matching dual evidence", "warning", "warning", true},
		{"legacy label only", nil, "warning", true},
		{"missing severity", nil, "", false},
		{"changed promoted severity", "critical", "", false},
		{"conflicting top level", "critical", "warning", false},
		{"conflicting label", "warning", "critical", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			var firing oscar.OrdinaryWebhookRequest
			posts := 0
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				var body oscar.OrdinaryWebhookRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Labels["severity"] != "warning" {
					t.Error("original severity missing from native payload")
				}
				if posts == 1 {
					firing = body
				} else if body.StartsAt != firing.StartsAt || body.Labels["oscar_fingerprint"] != "" {
					t.Error("native cleanup identity changed")
				}
				w.Header().Set("X-Oscar-Profile-Revision", oscar.OrdinaryProfileHash)
				w.Write([]byte(`{"status":"ok"}`))
			}))
			defer receiver.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/alerts/history" {
					t.Error("unexpected public mutation")
				}
				labels := []map[string]string{}
				for k, v := range firing.Labels {
					if k != "alertname" && k != "severity" {
						labels = append(labels, map[string]string{"Label": k, "Value": v})
					}
				}
				if tc.label != "" {
					labels = append(labels, map[string]string{"Label": "severity", "Value": tc.label})
				}
				row := map[string]any{"id": "probe", "alertname": firing.Labels["alertname"], "fingerprint": "fp", "startsAt": firing.StartsAt, "status": "firing", "labels": labels}
				if tc.top != nil {
					row["severity"] = tc.top
				}
				json.NewEncoder(w).Encode(map[string]any{"total_records": 1, "records": []any{row}})
			}))
			defer public.Close()
			client, err := oscar.New(domain.Target{BaseURL: public.URL, APIProfile: "public-v1", OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "KEY"}}}, oscar.Options{InputPath: "ordinary-webhook", Now: func() time.Time { return now }, Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			probe, err := client.ProbeLabelSurvivalRecorded(context.Background(), "crt_severity", "12345678", func(oscar.InjectionRequestEvidence) error { return nil })
			if tc.pass {
				if err != nil || !probe.CleanupForwarded || posts != 2 || client.UnresolvedInputCleanup() {
					t.Fatalf("valid observed severity refused: %+v err=%v posts=%d", probe, err, posts)
				}
			} else if err == nil || probe.CleanupForwarded || posts != 1 || !client.UnresolvedInputCleanup() {
				t.Fatalf("uncertain severity cleared: %+v err=%v posts=%d", probe, err, posts)
			}
			if tc.label == "" {
				if _, exists := probe.Labels["severity"]; exists {
					t.Error("promoted severity fabricated as a native label")
				}
			}
		})
	}
}
