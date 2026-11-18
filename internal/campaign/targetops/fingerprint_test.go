package targetops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Wire documents as OSCAR declares them (extra='forbid'): the fake decodes
// every request strictly, so an unknown field fails the test.
type fpExclusionWire struct {
	EntryID       string `json:"entry_id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	CaseSensitive *bool  `json:"case_sensitive,omitempty"`
	Scope         string `json:"scope,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

type fpDefinitionWire struct {
	SchemaVersion *int   `json:"schema_version,omitempty"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Enabled       *bool  `json:"enabled"`
	Scope         *struct {
		Operator string `json:"operator"`
		Children []struct {
			Field    string `json:"field"`
			Operator string `json:"operator"`
			Value    string `json:"value"`
		} `json:"children"`
	} `json:"scope"`
	Action     string            `json:"action"`
	Exclusions []fpExclusionWire `json:"exclusions"`
}

type fpPolicy struct {
	id, name                      string
	editVersion                   int
	draftRev, activeRev           string
	def                           fpDefinitionWire
	enabledByRev                  map[string]bool
	desiredRev                    string
	desiredEnabled, desiredListed bool
}

// fakeFP implements the fingerprint-policy management contract.
type fakeFP struct {
	t  *testing.T
	mu sync.Mutex

	published   bool
	pub         int // current desired publication generation
	loaded      int // consumers' loaded generation; catches up after lag polls
	lag, polls  int
	neverLoad   bool
	mode        string
	perms       []string
	policies    map[string]*fpPolicy
	revs        int
	previews    map[string]int // preview_id -> base generation
	staleOnce   bool           // the next activation finds another publisher's base
	staleEdits  int            // next N PUTs answer fingerprint_policy_stale_edit
	ops         map[string]bool
	calls       []string
	activations []string // operation ids
}

func newFakeFP(t *testing.T) *fakeFP {
	return &fakeFP{t: t, published: true, pub: 1, loaded: 1, mode: "managed", perms: []string{"manage:fingerprint-policies:scoped", "read:fingerprint-policies"},
		policies: map[string]*fpPolicy{}, previews: map[string]int{}, ops: map[string]bool{}}
}

func pubID(g int) string { return fmt.Sprintf("pub-%d", g) }

func (f *fakeFP) decode(r *http.Request, into any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		f.t.Errorf("%s %s: request body: %v", r.Method, r.URL.Path, err)
		return false
	}
	return true
}

func fpErr(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"detail":{"code":%q}}`, code)
}

func (f *fakeFP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := strings.CutPrefix(r.URL.Path, "/ext/mw/api/v1/fingerprint-policies")
	if !ok || strings.Contains(path, "bootstrap") || strings.Contains(path, "legacy") || strings.Contains(path, "global-defaults") {
		f.t.Errorf("forbidden call %s %s", r.Method, r.URL.Path)
		fpErr(w, 405, "fingerprint_route_not_allowed")
		return
	}
	f.calls = append(f.calls, r.Method+" "+path)
	switch {
	case r.Method == "GET" && path == "/publication-status":
		f.polls++
		if !f.published {
			w.Write([]byte(`{"desired":null,"converged":false,"state":"unpublished"}`))
			return
		}
		if !f.neverLoad && f.loaded != f.pub && f.polls > f.lag {
			f.loaded = f.pub
		}
		fmt.Fprintf(w, `{"desired":{"publication_id":%q,"generation":%d,"digest":"d"},"converged":%t,"consumers":{"state":"managed","parents":[{"slot":"alertmanager","children":[{"instance":"a1","serving_mode":%q,"loaded":{"publication_id":%q}},{"instance":"a2","serving_mode":"managed","loaded":{"publication_id":%q}}]}]}}`,
			pubID(f.pub), f.pub, f.loaded == f.pub, f.mode, pubID(f.loaded), pubID(f.loaded))
	case r.Method == "GET" && path == "/capabilities":
		json.NewEncoder(w).Encode(map[string]any{"permissions": f.perms})
	case r.Method == "GET" && path == "":
		var filt struct {
			Items []struct{ Field, Operator, Value string } `json:"items"`
		}
		if r.URL.Query().Get("kind") != "scoped" || json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filt) != nil ||
			len(filt.Items) != 1 || filt.Items[0].Field != "name" || filt.Items[0].Operator != "equals" {
			f.t.Errorf("list query %s", r.URL.RawQuery)
		}
		rows := []map[string]any{}
		for _, p := range f.policies {
			if len(filt.Items) == 1 && p.name == filt.Items[0].Value {
				rows = append(rows, map[string]any{"id": p.id, "name": p.name, "kind": "scoped", "edit_version": p.editVersion,
					"draft_revision_id": p.draftRev, "active_revision_id": p.activeRev, "enabled": *p.def.Enabled})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"rows": rows, "total": len(rows)})
	case r.Method == "POST" && path == "":
		var def fpDefinitionWire
		if !f.decode(r, &def) || !f.validDef(w, def) {
			return
		}
		for _, p := range f.policies {
			if p.name == def.Name {
				fpErr(w, 409, "policy_name_or_global_catalog_conflict")
				return
			}
		}
		f.revs++
		p := &fpPolicy{id: fmt.Sprintf("00000000-0000-0000-0000-%012d", len(f.policies)+1), name: def.Name, editVersion: 1,
			draftRev: fmt.Sprintf("rev-%d", f.revs), def: def, enabledByRev: map[string]bool{}}
		p.enabledByRev[p.draftRev] = *def.Enabled
		f.policies[p.id] = p
		fmt.Fprintf(w, `{"policy_id":%q,"revision_id":%q,"edit_version":1}`, p.id, p.draftRev)
	case r.Method == "PUT" && f.policies[strings.TrimPrefix(path, "/")] != nil:
		p := f.policies[strings.TrimPrefix(path, "/")]
		var body struct {
			ExpectedVersion int              `json:"expected_version"`
			Definition      fpDefinitionWire `json:"definition"`
		}
		if !f.decode(r, &body) || !f.validDef(w, body.Definition) {
			return
		}
		if f.staleEdits > 0 {
			f.staleEdits--
			p.editVersion++ // someone else edited
		}
		if body.ExpectedVersion != p.editVersion {
			fpErr(w, 409, "fingerprint_policy_stale_edit")
			return
		}
		f.revs++
		p.editVersion++
		p.draftRev, p.def = fmt.Sprintf("rev-%d", f.revs), body.Definition
		p.enabledByRev[p.draftRev] = *body.Definition.Enabled
		fmt.Fprintf(w, `{"policy_id":%q,"revision_id":%q,"edit_version":%d}`, p.id, p.draftRev, p.editVersion)
	case r.Method == "GET" && f.policies[strings.TrimPrefix(path, "/")] != nil:
		p := f.policies[strings.TrimPrefix(path, "/")]
		var active any
		if p.activeRev != "" {
			active = p.activeRev
		}
		json.NewEncoder(w).Encode(map[string]any{"id": p.id, "name": p.name, "kind": "scoped", "edit_version": p.editVersion,
			"draft_revision_id": p.draftRev, "active_revision_id": active, "draft": p.def})
	case r.Method == "POST" && path == "/preview":
		var body struct {
			Base     *string  `json:"base_publication_id"`
			Selected []string `json:"selected_revision_ids"`
			Samples  []struct {
				Labels map[string]string `json:"labels"`
			} `json:"samples"`
		}
		if !f.decode(r, &body) {
			return
		}
		if body.Base == nil {
			f.t.Error("preview with a null base")
			fpErr(w, 422, "invalid_fingerprint_request")
			return
		}
		if len(body.Samples) == 0 || len(body.Selected) != 1 {
			fpErr(w, 422, "invalid_fingerprint_request")
			return
		}
		for _, s := range body.Samples {
			if s.Labels["alertname"] != "CORRTEST_FP" || s.Labels["corrtest_volatile"] == "" || s.Labels["oscar_fingerprint"] != "" || s.Labels["fingerprint_exclude"] != "" {
				f.t.Errorf("sample %v does not match the scope", s.Labels)
			}
		}
		if *body.Base != pubID(f.pub) {
			fpErr(w, 409, "fingerprint_publication_stale_base")
			return
		}
		id := fmt.Sprintf("prev-%d", len(f.previews)+1)
		f.previews[id] = f.pub
		fmt.Fprintf(w, `{"preview_id":%q,"candidate_digest":%q,"expires_at":"2026-11-17T00:15:00Z","blockers":[],"warnings":["firing_clear_identity_mismatch"],"results":[]}`, id, strings.Repeat("a", 64))
	case r.Method == "POST" && path == "/activate":
		var body struct {
			Base         *string  `json:"base_publication_id"`
			Selected     []string `json:"selected_revision_ids"`
			Digest       string   `json:"candidate_digest"`
			PreviewID    string   `json:"preview_id"`
			Acknowledged []string `json:"acknowledged_warnings"`
			OperationID  string   `json:"operation_id"`
		}
		if !f.decode(r, &body) {
			return
		}
		f.activations = append(f.activations, body.OperationID)
		if f.ops[body.OperationID] {
			f.t.Errorf("operation id %s reused", body.OperationID)
		}
		f.ops[body.OperationID] = true
		if f.staleOnce {
			f.staleOnce = false
			f.pub++ // another publisher activated
			fpErr(w, 409, "fingerprint_publication_stale_base")
			return
		}
		if body.Base == nil || *body.Base != pubID(f.pub) {
			fpErr(w, 409, "fingerprint_publication_stale_base")
			return
		}
		if g, ok := f.previews[body.PreviewID]; !ok || g != f.pub {
			fpErr(w, 409, "fingerprint_preview_invalid")
			return
		}
		if len(body.Acknowledged) != 1 || body.Acknowledged[0] != "firing_clear_identity_mismatch" {
			fpErr(w, 422, "fingerprint_preview_acknowledgment_required")
			return
		}
		f.pub++
		for _, p := range f.policies {
			if p.draftRev == body.Selected[0] {
				p.activeRev, p.desiredRev, p.desiredEnabled, p.desiredListed = p.draftRev, p.draftRev, p.enabledByRev[p.draftRev], true
			}
		}
		f.polls = 0
		fmt.Fprintf(w, `{"publication_id":%q,"generation":%d,"digest":"d","published_revision_ids":[%q]}`, pubID(f.pub), f.pub, body.Selected[0])
	case r.Method == "GET" && path == "/publications/desired":
		var entries []map[string]any
		for _, p := range f.policies {
			if p.desiredListed {
				entries = append(entries, map[string]any{"policy_id": p.id, "revision_id": p.desiredRev, "name": p.name, "enabled": p.desiredEnabled})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"publication_id": pubID(f.pub), "generation": f.pub, "policies": entries})
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		fpErr(w, 405, "fingerprint_route_not_allowed")
	}
}

func (f *fakeFP) validDef(w http.ResponseWriter, d fpDefinitionWire) bool {
	ok := d.Kind == "scoped" && d.Action == "exclude" && d.Enabled != nil && d.Scope != nil && d.Scope.Operator == "all" &&
		len(d.Scope.Children) == 1 && d.Scope.Children[0] == struct {
		Field    string `json:"field"`
		Operator string `json:"operator"`
		Value    string `json:"value"`
	}{"alertname", "eq", "CORRTEST_FP"} &&
		len(d.Exclusions) == 1 && d.Exclusions[0].Name == "corrtest_volatile" && d.Exclusions[0].Kind == "exact" && d.Exclusions[0].EntryID != ""
	if !ok {
		f.t.Errorf("invalid definition %+v", d)
		fpErr(w, 422, "invalid_fingerprint_policy_definition")
	}
	return ok
}

func fastFPPoll(t *testing.T) {
	old := fingerprintPollInterval
	fingerprintPollInterval = time.Millisecond
	t.Cleanup(func() { fingerprintPollInterval = old })
}

func testFP(t *testing.T, f *fakeFP) FingerprintPolicies {
	fastFPPoll(t)
	return FingerprintPolicies{C: testClient(t, f)}
}

var testFPDef = IgnoreLabelPolicy("corrtest-fp-test", "CORRTEST_FP", "corrtest_volatile")

func TestFingerprintPreflightSkipsUnlessCommissioned(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeFP)
		want  string
	}{
		{"unpublished", func(f *fakeFP) { f.published = false }, "unpublished"},
		{"compatibility consumer", func(f *fakeFP) { f.mode = "compatibility" }, "compatibility"},
		{"no scoped grant", func(f *fakeFP) { f.perms = []string{"read:fingerprint-policies"} }, "manage:fingerprint-policies:scoped"},
		{"commissioned", func(*fakeFP) {}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFP(t)
			tc.setup(f)
			skip, err := testFP(t, f).Preflight(context.Background())
			if err != nil || (tc.want == "") != (skip == "") || !strings.Contains(skip, tc.want) {
				t.Fatalf("skip %q err %v, want %q", skip, err, tc.want)
			}
		})
	}
	// The real target today: no managed publication at all.
	var st PublicationStatus
	if err := json.Unmarshal([]byte(`{"desired": null, "converged": false, "state": "unpublished"}`), &st); err != nil || st.NotCommissioned() == "" {
		t.Fatalf("unpublished status read as commissioned: %v", err)
	}
}

func TestFingerprintEnableCreatesPublishesAndWaitsForConsumers(t *testing.T) {
	f := newFakeFP(t)
	f.lag = 3 // consumers refresh a few polls after the activation
	fp := testFP(t, f)
	pub, err := fp.Enable(context.Background(), testFPDef, "corrtest-camp_1-on", time.Second)
	if err != nil || pub != "pub-2" {
		t.Fatalf("pub %q err %v calls %v", pub, err, f.calls)
	}
	if f.loaded != 2 {
		t.Fatal("Enable returned before every consumer loaded the publication")
	}
	if len(f.policies) != 1 || len(f.activations) != 1 || !strings.HasPrefix(f.activations[0], "corrtest-camp_1-on") {
		t.Fatalf("policies %d activations %v", len(f.policies), f.activations)
	}
	// A second run reuses the fixed name: PUT with the current edit version.
	pub, err = fp.Enable(context.Background(), testFPDef, "corrtest-camp_2-on", time.Second)
	if err != nil || pub != "pub-3" || len(f.policies) != 1 {
		t.Fatalf("reuse: pub %q err %v policies %d", pub, err, len(f.policies))
	}
	if !contains(f.calls, "PUT /"+onlyPolicy(f).id) {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestFingerprintStaleBaseAndStaleEditAreRetried(t *testing.T) {
	f := newFakeFP(t)
	fp := testFP(t, f)
	if _, err := fp.Enable(context.Background(), testFPDef, "op-a", time.Second); err != nil {
		t.Fatal(err)
	}
	f.staleOnce, f.staleEdits = true, 1
	pub, err := fp.Enable(context.Background(), testFPDef, "op-b", time.Second)
	if err != nil || pub != pubID(f.pub) {
		t.Fatalf("pub %q err %v calls %v", pub, err, f.calls)
	}
	if n := len(f.activations); n != 3 || f.activations[1] == f.activations[2] {
		t.Fatalf("activations %v: want a retry with a new operation id after the stale base", f.activations)
	}
}

func TestFingerprintEnableConvergenceTimeout(t *testing.T) {
	f := newFakeFP(t)
	f.neverLoad = true
	_, err := testFP(t, f).Enable(context.Background(), testFPDef, "op", 20*time.Millisecond)
	if !errors.Is(err, ErrFingerprintNotConverged) {
		t.Fatalf("err %v", err)
	}
}

func TestFingerprintEnableNotManagedIsNotCommissioned(t *testing.T) {
	f := newFakeFP(t)
	f.mode = "compatibility"
	_, err := testFP(t, f).Enable(context.Background(), testFPDef, "op", time.Second)
	if !errors.Is(err, ErrFingerprintNotCommissioned) {
		t.Fatalf("err %v", err)
	}
}

func TestFingerprintDisableRevertsAndReadsBack(t *testing.T) {
	f := newFakeFP(t)
	fp := testFP(t, f)
	if err := fp.Disable(context.Background(), testFPDef, "off-0", time.Second); err != nil || len(f.activations) != 0 {
		t.Fatalf("disable of a missing policy: %v activations %v", err, f.activations)
	}
	if _, err := fp.Enable(context.Background(), testFPDef, "on", time.Second); err != nil {
		t.Fatal(err)
	}
	f.lag = 2
	if err := fp.Disable(context.Background(), testFPDef, "off", time.Second); err != nil {
		t.Fatalf("disable: %v calls %v", err, f.calls)
	}
	p := onlyPolicy(f)
	if *p.def.Enabled || p.activeRev != p.draftRev || !p.desiredListed || p.desiredEnabled || f.loaded != f.pub {
		t.Fatalf("not reverted: %+v loaded %d pub %d", p, f.loaded, f.pub)
	}
	// Idempotent: a clean policy needs no new publication.
	n := len(f.activations)
	if err := fp.Disable(context.Background(), testFPDef, "off-again", time.Second); err != nil || len(f.activations) != n {
		t.Fatalf("second disable: %v activations %v", err, f.activations)
	}
}

func TestFingerprintDisableThatNeverConvergesIsAnError(t *testing.T) {
	f := newFakeFP(t)
	fp := testFP(t, f)
	if _, err := fp.Enable(context.Background(), testFPDef, "on", time.Second); err != nil {
		t.Fatal(err)
	}
	f.neverLoad = true
	if err := fp.Disable(context.Background(), testFPDef, "off", 20*time.Millisecond); !errors.Is(err, ErrFingerprintNotConverged) {
		t.Fatalf("err %v", err)
	}
	// A cleanup retry must not read the unconverged disable as clean.
	if err := fp.Disable(context.Background(), testFPDef, "off-retry", 20*time.Millisecond); !errors.Is(err, ErrFingerprintNotConverged) {
		t.Fatalf("retry err %v", err)
	}
}

func TestFingerprintErrorCodeIsReadFromDetail(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fpErr(w, 409, "consumers_not_managed") }))
	_, err := FingerprintPolicies{C: c}.Status(context.Background())
	if fingerprintCode(err) != "consumers_not_managed" {
		t.Fatalf("err %v", err)
	}
}

func onlyPolicy(f *fakeFP) *fpPolicy {
	for _, p := range f.policies {
		return p
	}
	return nil
}
