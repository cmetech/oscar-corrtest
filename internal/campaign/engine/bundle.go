package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
)

const maxAttachment = 16 << 20

//go:embed campaign_report.html.tmpl
var reportTmpl string

var campaignReport = template.Must(template.New("campaign").Parse(reportTmpl))

func (r *run) foSnapshot() map[string][]score.FOSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]score.FOSample, len(r.fo))
	for k, v := range r.fo {
		out[k] = append([]score.FOSample(nil), v...)
	}
	return out
}

func (r *run) cohortSnapshot() []map[string]any {
	var out []map[string]any
	for _, m := range r.members() {
		out = append(out, map[string]any{"name": m.alert.Name, "policy": m.policy.spec.Key, "action": m.policy.spec.Action,
			"origin": m.origin, "deliveries": m.deliveries, "ambiguous": m.ambiguous})
	}
	return out
}

func attach(name string, content []byte) evidence.Attachment {
	d := sha256.Sum256(content)
	return evidence.Attachment{Name: name, Content: content, SHA256: hex.EncodeToString(d[:]), ByteSize: int64(len(content))}
}

func jsonAtt(name string, v any) (evidence.Attachment, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return attach(name, append(b, '\n')), err
}

func ndjsonAtt(name string, rows []any) (evidence.Attachment, error) {
	var buf bytes.Buffer
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			return evidence.Attachment{}, err
		}
		buf.Write(append(b, '\n'))
	}
	return attach(name, buf.Bytes()), nil
}

// ledgerAttachment gzips the ledger when it exceeds limit.
func ledgerAttachment(raw []byte, limit int) (evidence.Attachment, error) {
	if len(raw) <= limit {
		return attach("lane-ledger.ndjson", raw), nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return evidence.Attachment{}, err
	}
	if err := zw.Close(); err != nil {
		return evidence.Attachment{}, err
	}
	return attach("lane-ledger.ndjson.gz", buf.Bytes()), nil
}

// ExportCampaign writes the campaign evidence bundle for a completed campaign.
func (e *Engine) ExportCampaign(ctx context.Context, id, destination string) (evidence.Result, error) {
	c, err := e.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return evidence.Result{}, err
	}
	if c.Status != domain.CampaignCompleted {
		return evidence.Result{}, fmt.Errorf("campaign %s is not completed", id)
	}
	events, err := e.d.Store.RecentCampaignEvents(ctx, id, maxEvents)
	if err != nil {
		return evidence.Result{}, err
	}
	var missing []string
	note := func(what string, err error) {
		if err != nil {
			missing = append(missing, what+": "+err.Error())
		}
	}
	var report map[string]json.RawMessage
	note("campaign report", json.Unmarshal(c.Report, &report))
	var scenarioDoc json.RawMessage = json.RawMessage("null")
	var prechecks json.RawMessage = json.RawMessage("null")
	var guards []json.RawMessage
	for _, ev := range events {
		switch ev.Type {
		case scenarioEvent:
			scenarioDoc = ev.Detail
		case precheckEvent:
			prechecks = ev.Detail
		case "campaign.guard":
			guards = append(guards, ev.Detail)
		}
	}
	links, err := e.d.Store.CampaignRuns(ctx, id)
	if err != nil {
		return evidence.Result{}, err
	}
	var atts []evidence.Attachment
	var firstErr error
	put := func(a evidence.Attachment, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		atts = append(atts, a)
	}

	// Child correlation bundles, one per distinct linked run.
	var children []string
	seen := map[string]bool{}
	for _, l := range links {
		if seen[l.RunID] {
			continue
		}
		seen[l.RunID] = true
		content, err := e.childBundle(ctx, l.RunID)
		if err != nil {
			missing = append(missing, "correlation bundle "+l.RunID+": "+err.Error())
			continue
		}
		children = append(children, l.RunID)
		atts = append(atts, attach("correlation-"+l.RunID+".zip", content))
	}
	sort.Strings(children)

	raw := func(k string) json.RawMessage {
		if v, ok := report[k]; ok {
			return v
		}
		return json.RawMessage("null")
	}
	put(jsonAtt("prechecks.json", prechecks))
	put(jsonAtt("guards.json", guards))
	put(jsonAtt("metrics.json", raw("metrics")))
	put(jsonAtt("tickets.json", map[string]json.RawMessage{"tickets": raw("tickets"), "correlationTickets": raw("correlationTickets")}))
	put(jsonAtt("resend.json", raw("resend")))
	card := c.Scorecard
	if len(card) == 0 {
		card = json.RawMessage("null")
	}
	put(jsonAtt("scorecard.json", card))

	var cohorts []any
	var cm []json.RawMessage
	note("cohorts", json.Unmarshal(raw("cohorts"), &cm))
	for _, m := range cm {
		cohorts = append(cohorts, m)
	}
	put(ndjsonAtt("cohorts.ndjson", cohorts))
	var fo map[string][]score.FOSample
	note("front-office samples", json.Unmarshal(raw("frontoffice"), &fo))
	actions := make([]string, 0, len(fo))
	for a := range fo {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	var foRows []any
	for _, a := range actions {
		for _, s := range fo[a] {
			foRows = append(foRows, map[string]any{"action": a, "seconds": s.Seconds, "err": s.Err})
		}
	}
	put(ndjsonAtt("frontoffice.ndjson", foRows))

	var tr struct {
		LaneID string
		T0     time.Time
	}
	_ = json.Unmarshal(c.Report, &tr)
	if tr.LaneID != "" && !tr.T0.IsZero() { // traffic ran, so the ledger must exist
		ledger, err := os.ReadFile(filepath.Join(e.reportDir(), tr.LaneID+".ledger.jsonl"))
		if err != nil {
			return evidence.Result{}, fmt.Errorf("lane ledger unavailable: %w", err)
		}
		put(ledgerAttachment(ledger, maxAttachment))
	}

	var sc score.Scorecard
	if len(c.Scorecard) > 0 {
		note("scorecard", json.Unmarshal(c.Scorecard, &sc))
	}
	put(jsonAtt("campaign.json", map[string]any{"campaign": c.ID, "scenario": c.ScenarioID, "scenarioHash": c.ScenarioHash, "plan": scenarioDoc,
		"seed": seedOf(scenarioDoc), "trigger": c.Trigger, "runBy": c.RunBy, "status": c.Status, "verdict": c.Verdict, "cleanup": c.Cleanup,
		"comparable": c.Comparable, "startedAt": c.StartedAt, "endedAt": c.EndedAt, "correlationRuns": children,
		"complete": len(missing) == 0, "missing": append([]string{}, missing...)}))
	var html bytes.Buffer
	cardPretty, _ := json.MarshalIndent(json.RawMessage(card), "", "  ")
	guardsPretty, _ := json.MarshalIndent(guards, "", "  ")
	if err := campaignReport.Execute(&html, map[string]any{"ID": c.ID, "Scenario": c.ScenarioID, "Hash": c.ScenarioHash, "Status": c.Status,
		"Verdict": c.Verdict, "Cleanup": c.Cleanup, "Comparable": c.Comparable, "RunBy": c.RunBy, "Trigger": c.Trigger, "Missing": missing,
		"Reasons": sc.Reasons, "Headlines": sc.Headlines, "Guards": string(guardsPretty), "Scorecard": string(cardPretty), "Runs": children}); err != nil {
		return evidence.Result{}, err
	}
	put(attach("campaign-report.html", html.Bytes()), nil)
	if firstErr != nil {
		return evidence.Result{}, firstErr
	}

	canonical := c.Report
	if !json.Valid(canonical) {
		canonical = json.RawMessage("{}")
	}
	plan := scenarioDoc
	if !json.Valid(plan) {
		plan = json.RawMessage("{}")
	}
	ended := c.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	synthetic := domain.Run{ID: c.ID, ShortToken: domain.RunID(c.ID).Short(), ScenarioID: c.ScenarioID, Status: domain.RunCompleted, Verdict: c.Verdict,
		CleanupStatus: c.Cleanup, CompiledPlanJSON: plan, CanonicalReportJSON: canonical, CreatedAt: c.StartedAt, UpdatedAt: ended}
	return evidence.WriteWithArtifacts(ctx, destination, synthetic, nil, atts)
}

func (e *Engine) reportDir() string {
	if e.d.ReportDir != "" {
		return e.d.ReportDir
	}
	return os.TempDir()
}

func seedOf(plan json.RawMessage) any {
	var s struct{ Seed any }
	_ = json.Unmarshal(plan, &s)
	return s.Seed
}

func (e *Engine) childBundle(ctx context.Context, runID string) ([]byte, error) {
	if e.d.ExportRun == nil {
		return nil, errors.New("run export unavailable")
	}
	dir, err := os.MkdirTemp("", "campaign-child-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "run.zip")
	if _, err := e.d.ExportRun(ctx, runID, dest); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(dest) // #nosec G304 -- dest is a file this code just wrote inside the bundle directory.
	if err != nil {
		return nil, err
	}
	if len(b) > maxAttachment {
		return nil, errors.New("bundle exceeds 16 MiB")
	}
	return b, nil
}
