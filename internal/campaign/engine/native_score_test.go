package engine

import (
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAbsentTailAndSyntheticTicketSurviveAnotherFailure(t *testing.T) {
	s := scenario.AlertingDayV3()
	r := &run{id: "fixture", s: s, t0: time.Unix(1700000000, 0)}
	v := newNativeTally(r)
	for i := 0; i < 14; i++ {
		for _, lane := range []string{"POL", "REPEAT"} {
			for _, k := range []string{"native.managed.segment", "native.managed.segment_closed"} {
				v.segments[fmt.Sprintf("%s/%s/%d", k, lane, i)] = true
			}
		}
	}
	v.finish()
	card := score.Scorecard{Verdict: domain.VerdictPass}
	judge(&card, []string{"observed duplicate"}, v.Missing)
	reasons := strings.Join(card.Reasons, " ")
	if !card.EvidenceIncomplete || card.Verdict != domain.VerdictFail || !strings.Contains(reasons, "native.managed.segment_closed/POL/14") || !strings.Contains(reasons, "native.synthetic_ticket") {
		t.Fatal(card)
	}
}
func TestGroupedProviderTotalsUsePhysicalAndMemberDenominators(t *testing.T) {
	v := newNativeTally(&run{s: scenario.ManagedAlertsSmokeV1()})
	group := []targetops.Receipt{{ID: 19, Alerts: []targetops.SinkAlert{{AlertName: "A"}, {AlertName: "B"}}}}
	v.receipts(group, "A")
	v.receipts(group, "B")
	v.receipts(group, "A")
	v.finish()
	if v.PhysicalRequests != 1 || v.ProviderMemberReceipts != 2 {
		t.Fatal(v.nativeEvidence)
	}
}
func TestSmokeCannotPassOnLifecycleMarker(t *testing.T) {
	v := newNativeTally(&run{id: "fixture", s: scenario.ManagedAlertsSmokeV1()})
	v.consume(domain.CampaignEvent{Type: "native.managed.functional", Detail: mustJSON(map[string]bool{"completed": true})})
	v.finish()
	if len(v.Missing) == 0 || v.Facets["native.managed.functional"] != 1 {
		t.Fatal(v.nativeEvidence)
	}
}

func TestUncommissionedOrPartialTraceCannotScore(t *testing.T) {
	s := scenario.AlertingDayControlV1()
	r := &run{id: "camp_cost", s: s, t0: time.Unix(1700000000, 0), e: &Engine{d: Deps{ObserverDir: t.TempDir()}}}
	r.opts.Provenance = &domain.CampaignProvenance{TargetBuild: "oscar", CorrTestBuild: "corrtest", WorkloadConfig: "work"}
	raw := costFile{Run: r.id, Hash: s.Hash(), TargetBuild: "oscar", CorrTestBuild: "corrtest", WorkloadConfig: "work", ObserverConfig: strings.Repeat("b", 64), From: r.t0, Through: r.t0.Add(s.LoadEnd)}
	half, cpu := 0.5, 1.
	raw.SamplingFraction = &half
	raw.ObserverCPUSeconds = &cpu
	if err := os.WriteFile(filepath.Join(r.e.d.ObserverDir, r.id+".json"), mustJSON(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.costCapture(newNativeTally(r), nil); err == nil {
		t.Fatal("sampled observer qualified absence")
	}
	half = 1
	raw.Attempts = []attemptTrace{{OriginalDispatch: strings.Repeat("a", 64), TraceID: strings.Repeat("c", 32), ReceiptID: 1}}
	if err := os.WriteFile(filepath.Join(r.e.d.ObserverDir, r.id+".json"), mustJSON(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.costCapture(newNativeTally(r), nil); err == nil {
		t.Fatal("unmatched original physical contact qualified")
	}
}
