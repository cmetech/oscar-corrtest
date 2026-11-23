package lane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSilenceRecoveryUsesOnlyOwnedMatchAndOriginalID(t *testing.T) {
	const id = "398c2f8c-9d75-4e46-8265-3e9325543039"
	base := time.Now().UTC().Truncate(time.Millisecond)
	s := SuppressionSubject{AID: "CT-run-AID", Source: "test-probe", TID: "NE", Owner: "CorrTest-unique-comment", Start: base, End: base.Add(time.Minute)}
	var saved ownedSilence
	created, deleted := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/silences" && r.Method == "POST":
			created++
			if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
				t.Error(err)
			}
			if !s.matches(saved) {
				t.Error("silence not exact")
			}
			saved.ID = id
			saved.Status.State = "active"
			json.NewEncoder(w).Encode(map[string]string{"silenceID": id})
		case r.URL.Path == "/api/v2/silences" && r.Method == "GET":
			if r.URL.Query().Get("filter") == "" {
				t.Error("unscoped silence list")
			}
			json.NewEncoder(w).Encode([]ownedSilence{saved})
		case r.URL.Path == "/api/v2/silence/"+id && r.Method == "GET":
			json.NewEncoder(w).Encode(saved)
		case r.URL.Path == "/api/v2/silence/"+id && r.Method == "DELETE":
			deleted++
			saved.Status.State = "expired"
			w.WriteHeader(200)
		default:
			t.Error(r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := amSender{url: srv.URL + "/api/v2/alerts", client: srv.Client()}
	if got, err := c.Silence(context.Background(), s); err != nil || got != id {
		t.Fatal(got, err)
	}
	foreign := s
	foreign.Owner = "other"
	foreign.ControlID = id
	if err := c.ExpireSilence(context.Background(), foreign); err == nil || deleted != 0 {
		t.Fatal("foreign silence deleted")
	}
	// Recover the intent even if the original response was lost.
	if err := c.ExpireSilence(context.Background(), s); err != nil || created != 1 || deleted != 1 {
		t.Fatal(err, created, deleted)
	}
	if err := c.ExpireSilence(context.Background(), s); err != nil || deleted != 1 {
		t.Fatal("expiry replay", err)
	}
}
