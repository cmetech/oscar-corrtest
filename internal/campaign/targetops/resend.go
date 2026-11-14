package targetops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

var (
	ErrActivationTimeout   = errors.New("resend policy activation did not complete in time (inconclusive)")
	ErrReceiverUnavailable = errors.New("resend policy activation unavailable")
	ErrRevisionMismatch    = errors.New("resend policy revision mismatch")
)

// activationPollInterval is a var so tests can shorten it.
var activationPollInterval = 2 * time.Second

const resendPath = "/api/v1/resend-policies"

// PolicyDefinition uses OSCAR's exact field names. Enabled has no omitempty: drafts must say false.
type PolicyDefinition struct {
	NotifierID       string `json:"notifier_id"`
	Name             string `json:"name"`
	Action           string `json:"action"` // notification | ticket
	Mode             string `json:"mode"`   // time | occurrences | either
	IntervalSeconds  *int   `json:"interval_seconds,omitempty"`
	EveryOccurrences *int   `json:"every_occurrences,omitempty"`
	MaxResends       *int   `json:"max_resends,omitempty"`
	Priority         int    `json:"priority"`
	Condition        string `json:"condition,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type Policy struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	Status   string `json:"status"`
}

type PolicyStatus struct {
	Enrollment string `json:"enrollment"` // never_enrolled | preparing | enrolled | dormant | authority_unavailable
	Revision   int    `json:"revision"`
	Enabled    bool   `json:"enabled"`
	Episodes   struct {
		Open            int `json:"open"`
		Exhausted       int `json:"exhausted"`
		Orphaned        int `json:"orphaned"`
		CoveragePending int `json:"coverage_pending"`
	} `json:"episodes"`
	RecentPolicyEpisodes []struct {
		EpisodeNumber   int     `json:"episode_number"`
		UsedResends     int     `json:"used_resends"`
		StickyExhausted bool    `json:"sticky_exhausted"`
		LastAttemptAt   *string `json:"last_attempt_at"`
	} `json:"recent_policy_episodes"`
}

func mapResendErr(err error) error {
	var me *oscar.MachineError
	if !errors.As(err, &me) {
		return err
	}
	switch {
	case me.StatusCode == http.StatusServiceUnavailable &&
		(me.Detail == "RESEND_POLICY_OPS_RECEIVER_UNCONFIGURED" || me.Detail == "RESEND_POLICY_ACTIVATION_UNAVAILABLE"):
		return fmt.Errorf("%w: %s", ErrReceiverUnavailable, me.Detail)
	case me.StatusCode == http.StatusConflict && strings.Contains(me.Detail, "REVISION"):
		return fmt.Errorf("%w: %s", ErrRevisionMismatch, me.Detail)
	case me.StatusCode == http.StatusConflict:
		return &ConflictError{Code: me.Detail, Detail: me.Error()}
	}
	return err
}

// ConflictError is a 409 that is not a recognised revision mismatch. The middleware proxy reduces taskmanager
// detail to a RESEND_POLICY_* code (or RESEND_POLICY_REQUEST_FAILED), so a stale revision may surface here;
// callers that suspect staleness should re-read Status and retry with its Revision.
type ConflictError struct{ Code, Detail string }

func (e *ConflictError) Error() string { return "resend policy conflict " + e.Code + ": " + e.Detail }

// CreateDraft always sends enabled=false.
func CreateDraft(ctx context.Context, c *oscar.Client, def PolicyDefinition, expectedRevision int) (Policy, error) {
	def.Enabled = false
	var p Policy
	err := c.DoJSON(ctx, http.MethodPost, resendPath, nil, map[string]any{"definition": def, "expected_revision": expectedRevision}, &p)
	return p, mapResendErr(err)
}

type opBody struct {
	NotifierID string `json:"notifier_id"`
	Action     string `json:"action"`
	Revision   int    `json:"expected_revision"`
}

// Activate: first activation answers 202 preparing, then /status is polled until enrolled or wait elapses.
//
// A 503 RESEND_POLICY_BACKEND_UNAVAILABLE (or a transport error) is ambiguous: the activation may or may not have
// been applied, so callers must check Status before retrying.
func Activate(ctx context.Context, c *oscar.Client, id, notifierID, action string, expectedRevision int, wait time.Duration) (Policy, error) {
	var resp struct {
		Status   string `json:"status"`
		Revision int    `json:"revision"`
	}
	if err := c.DoJSON(ctx, http.MethodPost, resendPath+"/"+id+"/activate", nil, opBody{notifierID, action, expectedRevision}, &resp); err != nil {
		return Policy{}, mapResendErr(err)
	}
	if resp.Status == "active" {
		return Policy{ID: id, Revision: resp.Revision, Status: "active"}, nil
	}
	deadline := time.Now().Add(wait)
	for {
		st, err := Status(ctx, c, notifierID, action, id)
		if err != nil {
			return Policy{}, err
		}
		if st.Enrollment == "enrolled" && st.Enabled {
			return Policy{ID: id, Revision: st.Revision, Status: "active"}, nil
		}
		if !time.Now().Add(activationPollInterval).Before(deadline) {
			return Policy{}, ErrActivationTimeout
		}
		select {
		case <-ctx.Done():
			return Policy{}, ctx.Err()
		case <-time.After(activationPollInterval):
		}
	}
}

// Deactivate and Delete return the new catalog revision. Every later call on the same notifier/action must use
// the latest revision, not the one passed in.
func Deactivate(ctx context.Context, c *oscar.Client, id, notifierID, action string, expectedRevision int) (int, error) {
	var r struct {
		Revision int `json:"revision"`
	}
	err := c.DoJSON(ctx, http.MethodPost, resendPath+"/"+id+"/deactivate", nil, opBody{notifierID, action, expectedRevision}, &r)
	return r.Revision, mapResendErr(err)
}

func Delete(ctx context.Context, c *oscar.Client, id, notifierID, action string, expectedRevision int) (int, error) {
	q := url.Values{"notifier_id": {notifierID}, "action": {action}}
	var r struct {
		Revision int `json:"revision"`
	}
	err := c.DoJSON(ctx, http.MethodDelete, resendPath+"/"+id, q, map[string]int{"expected_revision": expectedRevision}, &r)
	return r.Revision, mapResendErr(err)
}

func Status(ctx context.Context, c *oscar.Client, notifierID, action, policyID string) (PolicyStatus, error) {
	q := url.Values{"notifier_id": {notifierID}, "action": {action}}
	if policyID != "" {
		q.Set("policy_id", policyID)
	}
	var st PolicyStatus
	err := c.DoJSON(ctx, http.MethodGet, resendPath+"/status", q, nil, &st)
	return st, mapResendErr(err)
}

// Preview asks OSCAR whether the definition's condition matches sample properties (read-only).
func Preview(ctx context.Context, c *oscar.Client, def PolicyDefinition, sample map[string]any) (bool, error) {
	var resp struct {
		Matched bool `json:"matched"`
	}
	err := c.DoJSON(ctx, http.MethodPost, resendPath+"/preview", nil, map[string]any{"definition": def, "sample_properties": sample}, &resp)
	return resp.Matched, mapResendErr(err)
}
