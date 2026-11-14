package targetops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCreateDraftNeverSendsEnabledTrue(t *testing.T) {
	var body map[string]any
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"id":"p1","revision":1,"status":"draft"}`))
	}))
	p, err := CreateDraft(context.Background(), c, PolicyDefinition{NotifierID: "n", Name: "x", Action: "notification", Mode: "time", Enabled: true}, 0)
	if err != nil || p.ID != "p1" {
		t.Fatal(p, err)
	}
	def := body["definition"].(map[string]any)
	if v, ok := def["enabled"]; !ok || v != false {
		t.Fatalf("enabled = %v, want explicit false", v)
	}
	if body["expected_revision"].(float64) != 0 {
		t.Fatal("expected_revision missing")
	}
}

func TestResendActivationTimeoutIsInconclusive(t *testing.T) {
	activationPollInterval = 5 * time.Millisecond
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(202)
			w.Write([]byte(`{"status":"preparing","preparation_id":"x","cutover_at":"t"}`))
			return
		}
		w.Write([]byte(`{"enrollment":"preparing","enabled":false,"revision":2}`))
	}))
	_, err := Activate(context.Background(), c, "p", "n", "notification", 1, 30*time.Millisecond)
	if !errors.Is(err, ErrActivationTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestResendActivationPollsUntilEnrolled(t *testing.T) {
	activationPollInterval = 5 * time.Millisecond
	polls := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(202)
			w.Write([]byte(`{"status":"preparing"}`))
			return
		}
		polls++
		if polls < 3 {
			w.Write([]byte(`{"enrollment":"preparing","revision":2}`))
			return
		}
		w.Write([]byte(`{"enrollment":"enrolled","enabled":true,"revision":3}`))
	}))
	p, err := Activate(context.Background(), c, "p", "n", "notification", 2, 2*time.Second)
	if err != nil || p.Revision != 3 || p.Status != "active" {
		t.Fatal(p, err)
	}
}

func TestActivation503MapsToReceiverUnavailable(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"detail":"RESEND_POLICY_OPS_RECEIVER_UNCONFIGURED"}`))
	}))
	if _, err := Activate(context.Background(), c, "p", "n", "ticket", 1, time.Second); !errors.Is(err, ErrReceiverUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestResend409IsRevisionMismatchAndDeleteShape(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Query().Get("notifier_id") != "n" || r.URL.Query().Get("action") != "ticket" {
			t.Errorf("bad delete %s %s", r.Method, r.URL)
		}
		w.WriteHeader(409)
		w.Write([]byte(`{"detail":"RESEND_POLICY_REVISION_CONFLICT"}`))
	}))
	if err := Delete(context.Background(), c, "p", "n", "ticket", 1); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusParsesEpisodes(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("policy_id") != "p" {
			t.Error("policy_id missing")
		}
		w.Write([]byte(`{"enrollment":"enrolled","enabled":true,"episodes":{"open":2,"exhausted":1},"recent_policy_episodes":[{"episode_number":1,"used_resends":3,"sticky_exhausted":true,"last_attempt_at":"2026-11-13T00:00:00Z"}]}`))
	}))
	st, err := Status(context.Background(), c, "n", "notification", "p")
	if err != nil || st.Episodes.Open != 2 || st.RecentPolicyEpisodes[0].UsedResends != 3 || !st.RecentPolicyEpisodes[0].StickyExhausted {
		t.Fatal(st, err)
	}
}
