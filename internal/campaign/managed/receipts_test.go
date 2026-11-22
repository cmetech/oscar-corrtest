package managed

import (
	"strings"
	"testing"
	"time"
)

func TestPreparedReceiptSectionsDecodePhysicalOwners(t *testing.T) {
	at := "2026-11-22T00:00:00.123456Z"
	sections := []string{at + "\n",
		`{"History":"h","Authority":"authority","Episode":"native","Generation":2,"Variant":3,"Starts":"2026-11-22T00:00:00.123456000Z","Fingerprint":"source","SourceSeverity":"major","Floor":"critical"}` + "\n",
		`{"Operation":"op","Logical":"root","Receiver":"canonical","ScopeAction":"notification","ScopeNotifier":"sink","Target":"target","Callback":"cb","Child":"child","Revision":"immutable","BindingID":"binding","ConfirmedAt":null}` + "\n",
		`{"root":"root"}` + "\n",
		`{"ID":"binding","Rule":"rule","Revision":"immutable","Notifier":"sink","Action":"notification","State":"bound","CatalogRevision":5}` + "\n",
		`{"Flow":"flow","Episode":2,"Audit":"audit","AuditStatus":"success","AuditIncident":"INC","Operation":"audit"}` + "\n", ""}
	raw := strings.Join(sections, "---\n")
	s, err := decodeSnapshot(raw, time.Millisecond)
	if err != nil || s.Source[0].Generation != 2 || s.Source[0].Floor != "critical" || s.Source[0].SourceSeverity != "major" || s.Effects[0].ScopeNotifier != "sink" || s.Effects[0].ConfirmedAt != "" || s.DispatchRoots[0] != "root" || s.Bindings[0].CatalogRevision != 5 || s.Tickets[0].Episode != 2 || s.MeasuredAt.Format(time.RFC3339Nano) != at {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{strings.TrimSuffix(raw, "---\n"), strings.Replace(raw, `"CatalogRevision":5`, `"CatalogRevision":"bad"`, 1), strings.Replace(raw, `{"root":"root"}`, `{"root":null}`, 1)} {
		if _, err := decodeSnapshot(bad, 0); err == nil {
			t.Fatal("invalid owner data accepted")
		}
	}
	for _, q := range []string{sourceSelect, effectSelect, dispatchSelect, bindingSelect, ticketSelect} {
		if strings.Count(q, "?") != 1 || !strings.HasPrefix(q, "SELECT ") || strings.Contains(q, "SELECT *") {
			t.Fatal("unbounded receipt projection")
		}
	}
}
