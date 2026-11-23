package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignedActorProjectsTicketAckAndMixedBulk(t *testing.T) {
	const h = "11111111-1111-4111-8111-111111111111"
	const h2 = "22222222-2222-4222-8222-222222222222"
	const req = "33333333-3333-4333-8333-333333333333"
	const req2 = "44444444-4444-4444-8444-444444444444"
	c := fixtureConfig(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=fixture" {
			t.Error("missing operator session")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/api/alertmanager/history/ticket":
			if body["alertId"] != h || body["request_id"] != req {
				t.Error(body)
			}
			fmt.Fprint(w, `{"ticket_id":"INC1","outcome":"succeeded","operation_id":"ticket-owner","ticket":{"internal":"hidden"}}`)
		case "/api/alertmanager/acknowledge":
			fmt.Fprintf(w, `{"record":{"ID":%q,"episode_number":7,"acknowledged":true,"annotations":{"signature":"hidden"}}}`, h)
		case "/api/alertmanager/resend-bulk":
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"results":[{"alert_id":%q,"request_id":%q,"status_code":409,"error":{"code":"NATIVE_EPISODE_RESOLVED"}},{"alert_id":%q,"request_id":%q,"resend_id":"original"}]}`, h, req, h2, req2)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	a, err := NewActor(c, srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := a.Command(context.Background(), "manual_ticket", h, req)
	if err != nil || receipt.TicketID != "INC1" || receipt.Operation != "ticket-owner" {
		t.Fatal(receipt, err)
	}
	ack, err := a.Command(context.Background(), "acknowledge", h, req)
	if err != nil || ack.Record.ID != h || ack.Record.Episode != 7 || !ack.Record.Acknowledged {
		t.Fatal(ack, err)
	}
	bulk, err := a.Bulk(context.Background(), []string{h, h2}, []string{req, req2})
	if err != nil || bulk.StatusCode != 202 || len(bulk.Results) != 2 || bulk.Results[0].Error.Code != "NATIVE_EPISODE_RESOLVED" || bulk.Results[1].RequestID != req2 {
		t.Fatal(bulk, err)
	}
	b, _ := json.Marshal([]CommandReceipt{receipt, ack, bulk})
	if strings.Contains(string(b), "hidden") {
		t.Fatal("raw signed actor response persisted")
	}
}
