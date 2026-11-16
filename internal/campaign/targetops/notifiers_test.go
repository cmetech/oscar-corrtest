package targetops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

type fakeNotifiers struct{ list []Notifier }

func (f *fakeNotifiers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ext/mw/api/v1/notifiers":
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		lo, hi := (p-1)*10, p*10 // server pages at 10 regardless of perPage
		if lo > len(f.list) {
			lo = len(f.list)
		}
		if hi > len(f.list) {
			hi = len(f.list)
		}
		json.NewEncoder(w).Encode(map[string]any{"total_records": len(f.list), "records": f.list[lo:hi]})
	case "/ext/mw/api/v1/notifiers/disable":
		var ids []string
		json.NewDecoder(r.Body).Decode(&ids)
		w.Write([]byte(`{}`)) // readback unchanged: simulates a silent no-op
	}
}

func TestExternalEnabledPagesAndKeeps(t *testing.T) {
	f := &fakeNotifiers{}
	for i := 0; i < 23; i++ {
		f.list = append(f.list, Notifier{ID: fmt.Sprint(i), Name: fmt.Sprintf("N%02d", i), Status: "enabled"})
	}
	f.list[3].Name, f.list[15].Name = "logger", "CAMPAIGN_SINK"
	f.list[5].Status = "disabled"
	got, err := ExternalEnabled(context.Background(), testClient(t, f), []string{"ALERTHISTORY", "LOGGER", "CORRELATION_EVENTS", "CAMPAIGN_SINK"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 23-3 {
		t.Fatalf("got %d, want 20", len(got))
	}
}

func TestSetNotifierStatusReadbackMismatchErrors(t *testing.T) {
	f := &fakeNotifiers{list: []Notifier{{ID: "a", Name: "X", Status: "enabled"}}}
	if _, err := SetNotifierStatus(context.Background(), testClient(t, f), "a", false); err == nil {
		t.Fatal("want readback mismatch error")
	}
}

type fakeNotifierCRUD struct {
	list    []Notifier
	created map[string]any
}

func (f *fakeNotifierCRUD) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/ext/mw/api/v1/notifiers":
		json.NewEncoder(w).Encode(map[string]any{"total_records": len(f.list), "records": f.list})
	case r.Method == http.MethodPost && r.URL.Path == "/ext/mw/api/v1/notifiers":
		json.NewDecoder(r.Body).Decode(&f.created)
		f.list = append(f.list, Notifier{ID: "dead-id", Name: f.created["name"].(string), Type: "webhook", Status: "enabled"})
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
	case r.Method == http.MethodDelete && r.URL.Path == "/ext/mw/api/v1/notifiers/dead-id":
		f.list = nil
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"detail":"not found"}`))
	}
}

func TestDeadNotifierCreateFindDelete(t *testing.T) {
	f := &fakeNotifierCRUD{}
	c := testClient(t, f)
	ctx := context.Background()
	n, err := CreateWebhookNotifier(ctx, c, "CAMPAIGN_DEAD_abc", "https://load.home.arpa:9/dead")
	if err != nil || n.ID != "dead-id" || n.Status != "enabled" {
		t.Fatalf("create %+v %v", n, err)
	}
	if wh := f.created["webhook_notifier"].(map[string]any); wh["url"] != "https://load.home.arpa:9/dead" || f.created["type"] != "webhook" {
		t.Fatalf("body %v", f.created)
	}
	if err := DeleteNotifier(ctx, c, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := FindNotifier(ctx, c, "CAMPAIGN_DEAD_abc"); err != ErrNotifierNotFound {
		t.Fatalf("find after delete: %v", err)
	}
	if err := DeleteNotifier(ctx, c, "gone"); err != nil {
		t.Fatalf("a missing notifier counts as deleted: %v", err)
	}
}
