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

func mixedOriginal() (OriginalReceipt, CommandReceipt) {
	sink := OriginalChild{History: "history", Episode: 3, Root: "root", Operation: "root", OpState: "confirmed", Kind: "resend", Target: "native-target", TargetState: "confirmed", Callback: "callback", Aggregate: "partial", ManifestOwned: 1, Expected: 2, Reported: 2, Name: "CAMPAIGN_SINK", Notifier: "sink-id", Action: "notification", ReceiptType: "NotificationDelivery", State: "sent", ProviderAction: "notification", ProviderOutcome: "sent", ProviderConfirmed: "2026-11-22T00:00:01.1"}
	ticket := sink
	ticket.Name, ticket.Notifier, ticket.Action, ticket.State, ticket.ProviderOutcome, ticket.ProviderConfirmed = "ALERTHISTORY", "history-id", "ticket", "suppressed", "suppressed", ""
	ticket.ProviderAction = "ticket"
	sink.Child, ticket.Child = sink.expectedID(), ticket.expectedID()
	status := CommandReceipt{AlertID: "history", RequestID: "request", Operation: "root", ResendID: "root", NativeState: "confirmed", TargetCounts: stateCounts{"confirmed": 1}, Counts: stateCounts{"partial": 1}}
	return OriginalReceipt{Children: []OriginalChild{sink, ticket}}, status
}

func TestOriginalCallbackSettlementAndSignedStatus(t *testing.T) {
	r, status := mixedOriginal()
	proof, ok := r.Resend(status, "history", 3, "request", "root")
	if !ok || proof.DisabledTicketNotification("sink-id") != r.Children[0].Child || proof.DisabledTicketNotification("different-sink") != "" {
		t.Fatal(proof, ok)
	}
	for name, breakProof := range map[string]func(*OriginalReceipt, *CommandReceipt){
		"reported unknown ticket": func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[1].State = "unknown_requires_reconciliation" },
		"missing reported ticket": func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[1].Child = "" },
		"missing expected child":  func(r *OriginalReceipt, _ *CommandReceipt) { r.Children = r.Children[:1] },
		"duplicate":               func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[1] = r.Children[0] },
		"wrong root":              func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[1].Root = "other" },
		"foreign manifest":        func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[0].ManifestOwned = 0 },
		"aggregate partial alone": func(r *OriginalReceipt, _ *CommandReceipt) { r.Children = nil },
		"unproved suppression":    func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[1].ProviderOutcome = "" },
		"failed without evidence": func(r *OriginalReceipt, _ *CommandReceipt) {
			r.Children[1].State = "failed"
			r.Children[1].ProviderOutcome = "failed"
		},
		"partial provider": func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[0].ProviderPartial = "true" },
		"unsettled successor": func(r *OriginalReceipt, _ *CommandReceipt) {
			next := r.Children[0]
			next.Parent, next.Operation, next.OpState = "root", "next", "held"
			r.Children = append(r.Children, next)
		},
		"missing successor":   func(r *OriginalReceipt, _ *CommandReceipt) { r.Children[0].Successor = "missing" },
		"wrong history":       func(_ *OriginalReceipt, s *CommandReceipt) { s.AlertID = "other" },
		"wrong request":       func(_ *OriginalReceipt, s *CommandReceipt) { s.RequestID = "other" },
		"wrong status counts": func(_ *OriginalReceipt, s *CommandReceipt) { s.Counts = stateCounts{"confirmed": 1} },
	} {
		t.Run(name, func(t *testing.T) {
			r, s := mixedOriginal()
			breakProof(&r, &s)
			if _, ok := r.Resend(s, "history", 3, "request", "root"); ok {
				t.Fatal("unresolved original settled")
			}
		})
	}
}
func TestKnownOriginalFailureReleasesCleanupOnly(t *testing.T) {
	r, status := mixedOriginal()
	ticket := &r.Children[1]
	ticket.State, ticket.ProviderOutcome, ticket.ProviderNoEffect = "failed", "failed", 1
	proof, ok := r.Resend(status, "history", 3, "request", "root")
	if !ok || proof.DisabledTicketNotification("sink-id") != "" {
		t.Fatal("known provider failure confused with successful workload", proof, ok)
	}
	ticket.ProviderConfirmed = "2026-11-22T00:00:02Z"
	if _, ok := r.Resend(status, "history", 3, "request", "root"); ok {
		t.Fatal("contradictory no-effect proof")
	}
}

func TestOriginalLeafAndPreEntryNoEffect(t *testing.T) {
	r, s := mixedOriginal()
	for i := range r.Children {
		r.Children[i].Operation, r.Children[i].Parent = "replacement", "root"
	}
	pre := OriginalChild{Root: "root", History: "history", Episode: 3, Operation: "root", OpState: "superseded_no_effect", Kind: "resend", Target: "native-target", TargetState: "no_effect", PreEntryNoEffect: 1, Successor: "replacement"}
	r.Children = append(r.Children, pre)
	s.NativeState = pre.OpState
	if _, ok := r.Resend(s, "history", 3, "request", "root"); !ok {
		t.Fatal("settled successor chain rejected")
	}
	r.Children[2].PreEntryNoEffect = 0
	if _, ok := r.Resend(s, "history", 3, "request", "root"); ok {
		t.Fatal("unproven pre-entry disposition accepted")
	}
}
func TestOriginalReceiptBoundAndProjection(t *testing.T) {
	row := `{"History":"h","Episode":2,"Root":"r","Operation":"op","ManifestOwned":1,"Child":null,"Held":0,"ProviderPartial":"false","Expected":2,"Reported":1}` + "\n"
	section := func(n int) string { return "2026-11-22T00:00:00Z\n---\n" + strings.Repeat(row, n) + "---\n" }
	r, err := decodeOriginal(section(1), time.Second)
	if err != nil || r.QueryDuration != time.Second || len(r.Children) != 1 || r.Children[0].Child != "" || r.Children[0].Expected != 2 || r.Children[0].Reported != 1 {
		t.Fatal(r, err)
	}
	if _, err := decodeOriginal(section(513), 0); err == nil {
		t.Fatal("truncated original accepted")
	}
	for _, fragment := range []string{"h.history_id=?", "seed.id=?", "o.logical_action_id=r.root", "LEFT JOIN AM_NativeActionTarget", "LEFT JOIN JSON_TABLE", "JSON_KEYS(p.manifest,'$.targets')", "JSON_QUOTE(k.name)", "'$.effects.'", "LIMIT 513"} {
		if !strings.Contains(originalSelect, fragment) {
			t.Fatal("lost original projection", fragment)
		}
	}
}
