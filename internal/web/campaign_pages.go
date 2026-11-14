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

	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	campaignscenario "github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

const precheckEventType = "campaign.precheck"

// campaignPage carries everything the three campaign templates need.
type campaignPage struct {
	Campaigns  []domain.Campaign
	Campaign   domain.Campaign
	Events     []domain.CampaignEvent
	Card       *score.Scorecard
	PrecheckID string
	Checks     []precheck.Result
	Pending    bool // pre-check campaign still running
	Blocked    bool
	Compare    compareResult
	IDs        string
	Terminal   bool
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
func loadPrechecks(ctx context.Context, ctl campaignController, id string) (checks []precheck.Result, pending bool, err error) {
	v, err := ctl.CampaignStatus(ctx, id)
	if err != nil {
		return nil, false, err
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
	return checks, checks == nil && !campaignTerminal(v.Campaign.Status), nil
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
		cp := &campaignPage{Campaigns: cs, PrecheckID: precheckID}
		if cp.PrecheckID == "" && len(cs) > 0 {
			cp.PrecheckID = cs[0].ID
		}
		if cp.PrecheckID != "" {
			cp.Checks, cp.Pending, err = loadPrechecks(r.Context(), ctl, cp.PrecheckID)
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
	begin := func(w http.ResponseWriter, r *http.Request, ack []string, only bool) (domain.Campaign, bool) {
		c, err := ctl.StartCampaign(r.Context(), appruntime.CampaignOptions{
			Scenario: campaignscenario.AlertingDayV1(), Trigger: "ui", TriggerIdentity: sessionIdentity(r),
			RunBy: "human", Acknowledged: ack, PrecheckOnly: only,
		})
		if errors.Is(err, appruntime.ErrCampaignActive) {
			list(w, r, r.PostFormValue("precheck"), err.Error(), http.StatusConflict)
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
		if c, ok := begin(w, r, nil, true); ok {
			http.Redirect(w, r, "/campaigns?precheck="+url.QueryEscape(c.ID), http.StatusSeeOther)
		}
	})
	mux.HandleFunc("POST /campaigns/start", func(w http.ResponseWriter, r *http.Request) {
		if !form(w, r) {
			return
		}
		pid := r.PostFormValue("precheck")
		checks, _, err := loadPrechecks(r.Context(), ctl, pid)
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
		if c, ok := begin(w, r, ack, false); ok {
			http.Redirect(w, r, "/campaigns/"+url.PathEscape(c.ID), http.StatusSeeOther)
		}
	})
	mux.HandleFunc("GET /campaigns/compare", func(w http.ResponseWriter, r *http.Request) {
		cp := &campaignPage{IDs: r.URL.Query().Get("ids")}
		if ids := splitIDs(cp.IDs); len(ids) >= 2 {
			res, err := compareCampaigns(r.Context(), ctl, ids)
			if err != nil {
				view(w, r, "campaign-compare", cp, err.Error(), http.StatusBadRequest)
				return
			}
			cp.Compare = res
		} else if cp.IDs != "" {
			view(w, r, "campaign-compare", cp, "Enter two or more campaign ids.", http.StatusBadRequest)
			return
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
