package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"strconv"
)

const precheckEventType = "campaign.precheck"

// campaignPage carries everything the three campaign templates need.
type campaignPage struct {
	Scenarios                       []string // built-in scenario refs (id@version) for the pre-check form
	Selected                        string   // scenario preselected from ?scenario= (the Scenarios page links here)
	Campaigns                       []domain.Campaign
	Campaign                        domain.Campaign
	Events                          []domain.CampaignEvent
	Card                            *score.Scorecard
	PrecheckID                      string
	Checks                          []precheck.Result
	Pending                         bool // pre-check campaign still running
	Blocked                         bool
	Compare                         score.Comparison
	IDs, ReferenceIDs, ScenarioHash string
	Mode                            score.ComparisonMode
	ProvenanceRoles                 []domain.CampaignRole
	Provenance                      *domain.CampaignProvenance
	Terminal                        bool
}

func campaignTerminal(s domain.CampaignStatus) bool {
	return s == domain.CampaignCompleted || s == domain.CampaignInterrupted
}

// sessionIdentity names the browser session without exposing the cookie.
func sessionIdentity(r *http.Request) string {
	c, err := r.Cookie("corrtest_session")
	if err != nil || c.Value == "" {
		return "session:local"
	}
	sum := sha256.Sum256([]byte(c.Value))
	return "session:" + hex.EncodeToString(sum[:])[:8]
}

// loadPrechecks reads the pre-check results recorded on a campaign.
func loadPrechecks(ctx context.Context, ctl campaignController, id string) (checks []precheck.Result, s campaignscenario.Scenario, pending bool, err error) {
	v, err := ctl.CampaignStatus(ctx, id)
	if err != nil {
		return nil, s, false, err
	}
	for _, e := range v.Events {
		if e.Type != precheckEventType {
			continue
		}
		var d struct{ Results []precheck.Result }
		if json.Unmarshal(e.Detail, &d) == nil {
			checks = d.Results
		}
	}
	if v.Scenario != nil {
		s = *v.Scenario
		if !lane.RegisteredWorkload(s, v.Campaign.ScenarioHash) {
			return nil, s, false, errors.New("recorded scenario hash mismatch")
		}
	} else {
		var ok bool
		s, ok = campaignscenario.ByHash(v.Campaign.ScenarioHash)
		if !ok || s.NativeWorkload != nil {
			return nil, s, false, errors.New("missing recorded scenario")
		}
	}
	return checks, s, checks == nil && !campaignTerminal(v.Campaign.Status), nil
}

func registerCampaignPages(mux *http.ServeMux, info version.Info, data DataSource, tmpl *template.Template, nonce nonceFunc, csrfSecret []byte, csrfErr error) {
	ctl, ok := data.(campaignController)
	if !ok {
		return
	}
	view := func(w http.ResponseWriter, r *http.Request, page string, cp *campaignPage, errMsg string, status int) {
		pd := pageData{Version: info, Page: page, Readiness: readiness(data), Campaign: cp, Error: errMsg}
		if csrfErr == nil {
			pd.CSRFToken = csrfToken(w, r, csrfSecret)
		}
		renderStatus(w, tmpl, nonce, status, pd)
	}
	// list renders /campaigns, with the pre-check results of precheckID (or the newest campaign).
	list := func(w http.ResponseWriter, r *http.Request, precheckID, errMsg string, status int) {
		cs, err := ctl.ListCampaigns(r.Context(), domain.CampaignFilter{Limit: 100})
		if err != nil {
			view(w, r, "campaigns", &campaignPage{}, err.Error(), http.StatusServiceUnavailable)
			return
		}
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].StartedAt.After(cs[j].StartedAt) })
		cp := &campaignPage{Campaigns: cs, PrecheckID: precheckID, Selected: r.URL.Query().Get("scenario")}
		for _, s := range campaignscenario.Builtins() {
			cp.Scenarios = append(cp.Scenarios, s.ID+"@"+s.Version)
		}
		if cp.PrecheckID == "" && len(cs) > 0 {
			cp.PrecheckID = cs[0].ID
		}
		if cp.PrecheckID != "" {
			var s campaignscenario.Scenario
			cp.Checks, s, cp.Pending, err = loadPrechecks(r.Context(), ctl, cp.PrecheckID)
			if err == nil && s.RequiresComparisonProvenance() {
				if s.NativeWorkload.ConfiguredPolicy {
					cp.ProvenanceRoles = []domain.CampaignRole{domain.CampaignConfiguredPolicyReference, domain.CampaignConfiguredPolicyCandidate}
				} else {
					cp.ProvenanceRoles = []domain.CampaignRole{domain.CampaignPreChangeControl, domain.CampaignPostChangeControl}
				}
			}
			if err != nil { // a stale id is not fatal
				cp.PrecheckID = ""
			}
		}
		for _, c := range cp.Checks {
			if c.Tier == precheck.Blocking && c.Status != precheck.Pass {
				cp.Blocked = true
			}
		}
		view(w, r, "campaigns", cp, errMsg, status)
	}
	form := func(w http.ResponseWriter, r *http.Request) bool {
		if csrfErr != nil {
			http.Error(w, "campaigns unavailable", http.StatusServiceUnavailable)
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return false
		}
		if !sameOrigin(r) || !validCSRF(r, csrfSecret) {
			http.Error(w, "request origin or CSRF token is invalid", http.StatusForbidden)
			return false
		}
		return true
	}
	begin := func(w http.ResponseWriter, r *http.Request, s campaignscenario.Scenario, ack []string, only bool, provenance *domain.CampaignProvenance) (domain.Campaign, bool) {
		c, err := ctl.StartCampaign(r.Context(), appruntime.CampaignOptions{
			Scenario: s, Trigger: "ui", TriggerIdentity: sessionIdentity(r),
			RunBy: "human", Acknowledged: ack, PrecheckOnly: only, Provenance: provenance,
		})
		if errors.Is(err, appruntime.ErrCampaignActive) || errors.Is(err, appruntime.ErrCampaignWorkload) {
			list(w, r, r.PostFormValue("precheck"), err.Error(), http.StatusConflict)
			return c, false
		}
		if errors.Is(err, appruntime.ErrCampaignProvenance) {
			list(w, r, r.PostFormValue("precheck"), err.Error(), http.StatusUnprocessableEntity)
			return c, false
		}
		if err != nil {
			list(w, r, r.PostFormValue("precheck"), err.Error(), http.StatusInternalServerError)
			return c, false
		}
		return c, true
	}

	mux.HandleFunc("GET /campaigns", func(w http.ResponseWriter, r *http.Request) {
		list(w, r, r.URL.Query().Get("precheck"), "", http.StatusOK)
	})
	mux.HandleFunc("POST /campaigns/prechecks", func(w http.ResponseWriter, r *http.Request) {
		if !form(w, r) {
			return
		}
		ref := r.PostFormValue("scenario")
		if ref == "" {
			ref = "alerting-day@v1"
		}
		s, ok := campaignscenario.Lookup(ref)
		if !ok {
			list(w, r, "", "Unknown scenario "+ref+".", http.StatusBadRequest)
			return
		}
		if raw := r.PostFormValue("managed_share"); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err == nil {
				s, err = lane.ConfigureShare(s, &v)
			}
			if err != nil {
				list(w, r, "", err.Error(), http.StatusBadRequest)
				return
			}
		}
		if c, ok := begin(w, r, s, nil, true, nil); ok {
			http.Redirect(w, r, "/campaigns?precheck="+url.QueryEscape(c.ID), http.StatusSeeOther)
		}
	})
	mux.HandleFunc("POST /campaigns/start", func(w http.ResponseWriter, r *http.Request) {
		if !form(w, r) {
			return
		}
		pid := r.PostFormValue("precheck")
		checks, s, _, err := loadPrechecks(r.Context(), ctl, pid)
		if err != nil || checks == nil {
			list(w, r, pid, "Run pre-checks first.", http.StatusUnprocessableEntity)
			return
		}
		var ack []string
		for _, c := range checks {
			if c.Status == precheck.Pass {
				continue
			}
			if c.Tier == precheck.Blocking {
				list(w, r, pid, "Blocking check "+c.ID+" is not passing.", http.StatusUnprocessableEntity)
				return
			}
			if !strings.EqualFold(strings.TrimSpace(r.PostFormValue("ack_"+c.ID)), c.ID) {
				list(w, r, pid, fmt.Sprintf("Type %s to acknowledge advisory %s.", c.ID, c.ID), http.StatusUnprocessableEntity)
				return
			}
			ack = append(ack, c.ID)
		}
		var provenance *domain.CampaignProvenance
		if s.RequiresComparisonProvenance() {
			provenance = &domain.CampaignProvenance{
				Role: domain.CampaignRole(r.PostFormValue("role")), TargetBuild: r.PostFormValue("target_build"),
				CorrTestBuild: r.PostFormValue("corrtest_build"), WorkloadConfig: r.PostFormValue("workload_config"),
			}
		}
		if c, ok := begin(w, r, s, ack, false, provenance); ok {
			http.Redirect(w, r, "/campaigns/"+url.PathEscape(c.ID), http.StatusSeeOther)
		}
	})
	mux.HandleFunc("GET /campaigns/compare", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		req := comparisonRequest(query)
		cp := &campaignPage{IDs: query.Get("ids"), ReferenceIDs: query.Get("referenceIds"), ScenarioHash: req.scenarioHash, Mode: req.mode}
		if len(query) != 0 {
			res, err := compareCampaigns(r.Context(), ctl, req)
			if err != nil {
				status := http.StatusServiceUnavailable
				switch {
				case errors.Is(err, score.ErrMissingBaseline), errors.Is(err, score.ErrIncompatibleComparison):
					status = http.StatusBadRequest
				case errors.Is(err, appruntime.ErrCampaignNotFound):
					status = http.StatusNotFound
				}
				view(w, r, "campaign-compare", cp, err.Error(), status)
				return
			}
			cp.Compare = res
		}
		view(w, r, "campaign-compare", cp, "", http.StatusOK)
	})
	mux.HandleFunc("GET /campaigns/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := ctl.CampaignStatus(r.Context(), r.PathValue("id"))
		if errors.Is(err, appruntime.ErrCampaignNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			view(w, r, "campaign", &campaignPage{}, err.Error(), http.StatusServiceUnavailable)
			return
		}
		cp := &campaignPage{Campaign: v.Campaign, Events: v.Events, Terminal: campaignTerminal(v.Campaign.Status)}
		if v.Options != nil {
			cp.Provenance = v.Options.Provenance
		}
		if card, ok := parseCard(v.Campaign); ok {
			cp.Card = &card
		}
		view(w, r, "campaign", cp, "", http.StatusOK)
	})
	mux.HandleFunc("POST /campaigns/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if !form(w, r) {
			return
		}
		id := r.PathValue("id")
		if err := ctl.StopCampaign(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/campaigns/"+url.PathEscape(id), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /campaigns/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		if x, ok := data.(campaignExporter); ok {
			writeCampaignBundle(w, r, x, r.PathValue("id"))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /campaigns/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		var after int
		_, _ = fmt.Sscanf(r.URL.Query().Get("after"), "%d", &after) // bad input leaves after=0 (replay from start)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		disableStreamingDeadline(w)
		flusher, _ := w.(http.Flusher)
		for {
			v, err := ctl.CampaignStatus(r.Context(), r.PathValue("id"))
			if err != nil {
				return
			}
			for _, e := range v.Events {
				if e.Sequence <= after {
					continue
				}
				b, _ := json.Marshal(map[string]any{"sequence": e.Sequence, "type": e.Type, "level": e.Level, "summary": e.Summary, "occurredAt": e.OccurredAt.UTC().Format(time.RFC3339)})
				_, _ = fmt.Fprintf(w, "id: %d\nevent: campaign-event\ndata: %s\n\n", e.Sequence, b)
				after = e.Sequence
			}
			if flusher != nil {
				flusher.Flush()
			}
			if campaignTerminal(v.Campaign.Status) {
				_, _ = fmt.Fprint(w, "event: campaign-done\ndata: {}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	})
}
