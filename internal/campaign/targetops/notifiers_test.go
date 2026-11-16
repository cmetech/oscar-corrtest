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
