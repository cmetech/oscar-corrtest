package targetops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Fingerprint policies (OSCAR /api/v1/fingerprint-policies). A scoped policy
// changes the OSCAR fingerprint only once it is in a managed publication that
// every consumer has loaded; with no publication, or a consumer in
// compatibility mode, policies have no effect. There is no DELETE: a policy
// is reverted by publishing it disabled, and its row stays forever. This
// client never calls /bootstrap*, global-defaults edits or /legacy*, and
// never previews against a null base.

const fingerprintPath = "/api/v1/fingerprint-policies"

var (
	// ErrFingerprintNotCommissioned: fingerprint policies cannot take effect on this target.
	ErrFingerprintNotCommissioned = errors.New("fingerprint policies not commissioned")
	// ErrFingerprintNotConverged: consumers did not load the publication within the wait.
	ErrFingerprintNotConverged = errors.New("fingerprint publication did not converge in time")
)

// fingerprintPollInterval is a var so tests can shorten it (consumers refresh about every 30 s).
var fingerprintPollInterval = 5 * time.Second

// fingerprintTries bounds stale-base and stale-edit retries.
const fingerprintTries = 3

const scopedGrant = "manage:fingerprint-policies:scoped"

// FingerprintDefinition is a scoped exclude policy in OSCAR's field names;
// OSCAR forbids unknown fields, so only these are sent. Enabled has no omitempty.
type FingerprintDefinition struct {
	Kind       string                 `json:"kind"`
	Name       string                 `json:"name"`
	Enabled    bool                   `json:"enabled"`
	Action     string                 `json:"action"`
	Scope      FingerprintScope       `json:"scope"`
	Exclusions []FingerprintExclusion `json:"exclusions"`
}

type FingerprintScope struct {
	Operator string                 `json:"operator"`
	Children []FingerprintPredicate `json:"children"`
}

type FingerprintPredicate struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type FingerprintExclusion struct {
	EntryID string `json:"entry_id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
}

// IgnoreLabelPolicy is a scoped policy named name: alerts named alertname
// leave label out of their fingerprint. label must not be alertname.
func IgnoreLabelPolicy(name, alertname, label string) FingerprintDefinition {
	return FingerprintDefinition{Kind: "scoped", Name: name, Enabled: true, Action: "exclude",
		Scope:      FingerprintScope{Operator: "all", Children: []FingerprintPredicate{{Field: "alertname", Operator: "eq", Value: alertname}}},
		Exclusions: []FingerprintExclusion{{EntryID: "corrtest-ignore", Name: label, Kind: "exact"}}}
}

// sample is a preview sample matching def's scope and carrying its excluded labels.
func (d FingerprintDefinition) sample() map[string]any {
	labels := map[string]string{}
	for _, c := range d.Scope.Children {
		if c.Operator == "eq" {
			labels[c.Field] = c.Value
		}
	}
	for _, e := range d.Exclusions {
		labels[e.Name] = "corrtest"
	}
	return map[string]any{"labels": labels}
}

// PublicationStatus is GET /publication-status.
type PublicationStatus struct {
	Desired *struct {
		PublicationID string `json:"publication_id"`
	} `json:"desired"`
	Converged bool   `json:"converged"`
	State     string `json:"state"`
	Consumers struct {
		Parents []struct {
			Slot     string          `json:"slot"`
			Children []consumerChild `json:"children"`
		} `json:"parents"`
	} `json:"consumers"`
}

type consumerChild struct {
	Instance    string `json:"instance"`
	Membership  string `json:"membership"` // pending, live, draining, exited_clean, lost
	ServingMode string `json:"serving_mode"`
	Freshness   string `json:"freshness"`
	Loaded      *struct {
		PublicationID string `json:"publication_id"`
	} `json:"loaded"`
}

// reporting is false for a child that has exited, or has not reported yet
// (a worker Celery autoscale just spawned); such a child neither
// disqualifies nor counts as a consumer.
func (c consumerChild) reporting() bool {
	switch {
	case c.Membership == "exited_clean", c.Membership == "lost":
		return false
	case c.Freshness == "missing", c.ServingMode == "unknown", c.ServingMode == "":
		return false
	}
	return true
}

// NotCommissioned says why policies cannot take effect, "" when they can:
// a desired publication exists, at least one consumer reports and every
// reporting consumer serves managed.
func (s PublicationStatus) NotCommissioned() string {
	if s.State == "unpublished" || s.Desired == nil || s.Desired.PublicationID == "" {
		return "fingerprint policies unpublished (no desired publication)"
	}
	n := 0
	for _, p := range s.Consumers.Parents {
		for _, c := range p.Children {
			if !c.reporting() {
				continue
			}
			n++
			if c.ServingMode != "managed" {
				return fmt.Sprintf("consumer %s/%s serves %s, not managed", p.Slot, c.Instance, c.ServingMode)
			}
		}
	}
	if n == 0 {
		return "no fingerprint policy consumers reported"
	}
	return ""
}

// ServedBy reports whether pub is desired, converged and loaded by every reporting consumer.
func (s PublicationStatus) ServedBy(pub string) bool {
	if s.NotCommissioned() != "" || !s.Converged || s.Desired.PublicationID != pub {
		return false
	}
	for _, p := range s.Consumers.Parents {
		for _, c := range p.Children {
			if c.reporting() && (c.Loaded == nil || c.Loaded.PublicationID != pub) {
				return false
			}
		}
	}
	return true
}

// fingerprintCode is OSCAR's error code ({"detail":{"code":...}}), or "".
func fingerprintCode(err error) string {
	var me *oscar.MachineError
	if errors.As(err, &me) && me.StatusCode != 0 {
		return me.Detail
	}
	return ""
}

// FingerprintPolicies manages fingerprint policies with the campaign's API key.
type FingerprintPolicies struct{ C *oscar.Client }

func (f FingerprintPolicies) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	return f.C.DoJSON(ctx, method, fingerprintPath+path, q, in, out)
}

func (f FingerprintPolicies) Status(ctx context.Context) (PublicationStatus, error) {
	var st PublicationStatus
	return st, f.do(ctx, http.MethodGet, "/publication-status", nil, nil, &st)
}

// Preflight says why the lane must be skipped, "" when policies are
// commissioned and this key may manage scoped policies.
func (f FingerprintPolicies) Preflight(ctx context.Context) (string, error) {
	st, err := f.Status(ctx)
	if err != nil {
		return "", err
	}
	if why := st.NotCommissioned(); why != "" {
		return why, nil
	}
	var caps struct {
		Permissions []string `json:"permissions"`
	}
	if err := f.do(ctx, http.MethodGet, "/capabilities", nil, nil, &caps); err != nil {
		return "", err
	}
	for _, p := range caps.Permissions {
		if p == scopedGrant {
			return "", nil
		}
	}
	return "API key lacks " + scopedGrant, nil
}

type fingerprintPolicy struct {
	ID               string                `json:"id"`
	EditVersion      int                   `json:"edit_version"`
	DraftRevisionID  string                `json:"draft_revision_id"`
	ActiveRevisionID *string               `json:"active_revision_id"`
	Draft            FingerprintDefinition `json:"draft"`
}

// find returns the scoped policy named name (names are unique per kind), or nil.
func (f FingerprintPolicies) find(ctx context.Context, name string) (*fingerprintPolicy, error) {
	filters, _ := json.Marshal(map[string]any{"items": []any{map[string]string{"field": "name", "operator": "equals", "value": name}}})
	var resp struct {
		Rows []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rows"`
	}
	if err := f.do(ctx, http.MethodGet, "", url.Values{"kind": {"scoped"}, "filters": {string(filters)}}, nil, &resp); err != nil {
		return nil, err
	}
	for _, r := range resp.Rows {
		if r.Name == name {
			return f.get(ctx, r.ID)
		}
	}
	return nil, nil
}

func (f FingerprintPolicies) get(ctx context.Context, id string) (*fingerprintPolicy, error) {
	var p fingerprintPolicy
	if err := f.do(ctx, http.MethodGet, "/"+url.PathEscape(id), nil, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// save writes def as the draft of the policy with its name (creating it when
// absent) and returns the policy id and the new draft revision.
func (f FingerprintPolicies) save(ctx context.Context, def FingerprintDefinition) (id, rev string, err error) {
	var out struct {
		PolicyID   string `json:"policy_id"`
		RevisionID string `json:"revision_id"`
	}
	for try := 0; try < fingerprintTries; try++ {
		var p *fingerprintPolicy
		if p, err = f.find(ctx, def.Name); err != nil {
			return "", "", err
		}
		if p == nil {
			err = f.do(ctx, http.MethodPost, "", nil, def, &out)
		} else {
			err = f.do(ctx, http.MethodPut, "/"+url.PathEscape(p.ID), nil, map[string]any{"expected_version": p.EditVersion, "definition": def}, &out)
		}
		switch fingerprintCode(err) {
		case "fingerprint_policy_stale_edit", "policy_name_or_global_catalog_conflict":
			continue // edited or created concurrently: re-read and retry
		}
		return out.PolicyID, out.RevisionID, err
	}
	return "", "", err
}

// publish previews and activates rev on the current base, re-reading the base
// when another publisher activated first. Each attempt has its own operation id.
func (f FingerprintPolicies) publish(ctx context.Context, def FingerprintDefinition, rev, opID string) (string, error) {
	var err error
	for try := 1; try <= fingerprintTries; try++ {
		var st PublicationStatus
		if st, err = f.Status(ctx); err != nil {
			return "", err
		}
		if why := st.NotCommissioned(); why != "" {
			return "", fmt.Errorf("%w: %s", ErrFingerprintNotCommissioned, why)
		}
		base := st.Desired.PublicationID
		var pv struct {
			PreviewID       string   `json:"preview_id"`
			CandidateDigest string   `json:"candidate_digest"`
			Blockers        []string `json:"blockers"`
			Warnings        []string `json:"warnings"`
		}
		err = f.do(ctx, http.MethodPost, "/preview", nil, map[string]any{"base_publication_id": base,
			"selected_revision_ids": []string{rev}, "samples": []any{def.sample()}}, &pv)
		if fingerprintCode(err) == "fingerprint_publication_stale_base" {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(pv.Blockers) > 0 {
			return "", fmt.Errorf("fingerprint preview blocked: %s", strings.Join(pv.Blockers, ","))
		}
		op := fmt.Sprintf("%s-%d", opID, try)
		var rcpt struct {
			PublicationID string `json:"publication_id"`
		}
		err = f.do(ctx, http.MethodPost, "/activate", nil, map[string]any{"base_publication_id": base, "selected_revision_ids": []string{rev},
			"candidate_digest": pv.CandidateDigest, "preview_id": pv.PreviewID, "acknowledged_warnings": append([]string{}, pv.Warnings...),
			"operation_id": op}, &rcpt)
		switch code := fingerprintCode(err); {
		case code == "fingerprint_publication_stale_base":
			continue
		case code == "consumers_not_managed":
			return "", fmt.Errorf("%w: %s", ErrFingerprintNotCommissioned, code)
		case err != nil && (code == "" || isServerError(err)):
			// Ambiguous: the activation may have committed. The operation receipt says.
			var o struct {
				State         string  `json:"state"`
				PublicationID *string `json:"publication_id"`
			}
			if f.do(ctx, http.MethodGet, "/operations/"+url.PathEscape(op), nil, nil, &o) == nil && o.State == "committed" && o.PublicationID != nil {
				return *o.PublicationID, nil
			}
		}
		return rcpt.PublicationID, err
	}
	return "", err
}

func isServerError(err error) bool {
	var me *oscar.MachineError
	return errors.As(err, &me) && me.StatusCode >= 500
}

// WaitServed polls until every consumer has loaded pub, or wait elapses.
func (f FingerprintPolicies) WaitServed(ctx context.Context, pub string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		st, err := f.Status(ctx)
		if err == nil && st.ServedBy(pub) {
			return nil
		}
		if !time.Now().Add(fingerprintPollInterval).Before(deadline) {
			return fmt.Errorf("%w: %s within %s", ErrFingerprintNotConverged, pub, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fingerprintPollInterval):
		}
	}
}

// Enable saves def enabled, publishes it and waits until every consumer
// serves the publication; it returns the publication id. Alerts admitted
// before that use the previous snapshot.
func (f FingerprintPolicies) Enable(ctx context.Context, def FingerprintDefinition, opID string, wait time.Duration) (string, error) {
	def.Enabled = true
	_, rev, err := f.save(ctx, def)
	if err != nil {
		return "", err
	}
	pub, err := f.publish(ctx, def, rev, opID)
	if err != nil {
		return "", err
	}
	return pub, f.WaitServed(ctx, pub, wait)
}

type desiredEntry struct {
	PolicyID   string `json:"policy_id"`
	RevisionID string `json:"revision_id"`
	Enabled    bool   `json:"enabled"`
}

func (f FingerprintPolicies) desiredEntry(ctx context.Context, id string) (*desiredEntry, string, error) {
	var d struct {
		PublicationID string         `json:"publication_id"`
		Policies      []desiredEntry `json:"policies"`
	}
	if err := f.do(ctx, http.MethodGet, "/publications/desired", nil, nil, &d); err != nil {
		return nil, "", err
	}
	for _, e := range d.Policies {
		if e.PolicyID == id {
			return &e, d.PublicationID, nil
		}
	}
	return nil, d.PublicationID, nil
}

// Disable reverts the policy named def.Name (forward-only): its draft is saved
// disabled and, when the desired publication still carries another revision
// of it, the disabled one is published and served. It reads back
// draft.enabled=false, active=draft and a disabled desired entry. A policy
// that never existed or is already reverted (and served) needs no change.
func (f FingerprintPolicies) Disable(ctx context.Context, def FingerprintDefinition, opID string, wait time.Duration) error {
	def.Enabled = false
	p, err := f.find(ctx, def.Name)
	if err != nil || p == nil {
		return err
	}
	rev := p.DraftRevisionID
	if p.Draft.Enabled {
		if _, rev, err = f.save(ctx, def); err != nil {
			return err
		}
	}
	entry, pub, err := f.desiredEntry(ctx, p.ID)
	if err != nil {
		return err
	}
	if entry != nil && (entry.Enabled || entry.RevisionID != rev) {
		if pub, err = f.publish(ctx, def, rev, opID); err != nil {
			return err
		}
	}
	if p, err = f.get(ctx, p.ID); err != nil {
		return err
	}
	if entry, pub, err = f.desiredEntry(ctx, p.ID); err != nil {
		return err
	}
	if p.Draft.Enabled || entry != nil && (entry.Enabled || p.ActiveRevisionID == nil || *p.ActiveRevisionID != p.DraftRevisionID) {
		return fmt.Errorf("fingerprint policy %s read back still enabled or not the active revision", def.Name)
	}
	if entry == nil {
		return nil // never published: never in force
	}
	st, err := f.Status(ctx)
	if err != nil {
		return err
	}
	if st.NotCommissioned() != "" {
		return nil // consumers do not serve managed policies: it cannot be in force
	}
	return f.WaitServed(ctx, pub, wait)
}
