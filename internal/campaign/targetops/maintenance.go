package targetops

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Maintenance mode (OSC-189) through OSCAR's public middleware API
// (/notifiers/maintenance, oscar-middleware routers/maintenance.py). Every
// route is permission-checked against the API key's user role
// (core/route_permissions.py); manage:maintenance-windows covers them all.

const maintPath = "/notifiers/maintenance"

// MaintMatch is one pattern criterion: label operator value.
type MaintMatch struct {
	Label    string `json:"label"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// MaintPattern is a maintenance pattern (label matchers).
type MaintPattern struct {
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Match       []MaintMatch `json:"match"`
	Combinator  string       `json:"combinator"`
	AlwaysApply bool         `json:"always_apply"`
}

// MaintTemplate is a maintenance template (it only seeds windows).
type MaintTemplate struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	ServerSelector  map[string]any `json:"server_selector"`
	Servers         []string       `json:"servers"`
	Patterns        []string       `json:"patterns"`
	DefaultHandlers []string       `json:"default_handlers"`
}

// MaintWindow is a scheduled maintenance window request (POST /enable).
type MaintWindow struct {
	CR          string    `json:"cr_number"`
	Description string    `json:"description,omitempty"`
	Servers     []string  `json:"servers"`
	Patterns    []string  `json:"patterns"`
	Handlers    []string  `json:"handlers"`
	Timezone    string    `json:"timezone"`
	Start       time.Time `json:"start_time"`
	End         time.Time `json:"end_time"`
	TemplateKey string    `json:"template_key,omitempty"`
}

// MaintEnabled is the enable answer: what OSCAR actually applied.
type MaintEnabled struct {
	CR       string   `json:"cr_number"`
	Status   string   `json:"status"`
	Patterns []string `json:"patterns"`
	Handlers []string `json:"handlers"`
}

// MaintStatus is GET /status/{cr}: State is scheduled, active or expired
// while a window exists (an expired one keeps its rules until disabled);
// State and SuppressionID are empty when OSCAR has no window for the CR.
type MaintStatus struct {
	Active        bool       `json:"is_active"`
	State         string     `json:"state"`
	SuppressionID string     `json:"suppression_id"`
	Start         *time.Time `json:"start_time"`
	End           *time.Time `json:"end_time"`
}

// Absent reports that OSCAR holds no window for the CR.
func (s MaintStatus) Absent() bool { return s.State == "" && s.SuppressionID == "" && s.Start == nil }

// MaintRecord is one maintenance ledger row (every window ever created).
type MaintRecord struct {
	ID     string `json:"id"`
	CR     string `json:"cr_number"`
	Status string `json:"status"`
}

// MaintPatterns lists every pattern.
func (u UserAPI) MaintPatterns(ctx context.Context) ([]MaintPattern, error) {
	var out struct {
		Items []MaintPattern `json:"items"`
	}
	return out.Items, u.do(ctx, http.MethodGet, maintPath+"/patterns", nil, nil, &out)
}

// MaintTemplates lists every template.
func (u UserAPI) MaintTemplates(ctx context.Context) ([]MaintTemplate, error) {
	var out struct {
		Items []MaintTemplate `json:"items"`
	}
	return out.Items, u.do(ctx, http.MethodGet, maintPath+"/templates", nil, nil, &out)
}

// CreateMaintPattern creates p (409 when the key exists).
func (u UserAPI) CreateMaintPattern(ctx context.Context, p MaintPattern) error {
	return u.do(ctx, http.MethodPost, maintPath+"/patterns", nil, p, nil)
}

// CreateMaintTemplate creates t (409 when the key exists).
func (u UserAPI) CreateMaintTemplate(ctx context.Context, t MaintTemplate) error {
	return u.do(ctx, http.MethodPost, maintPath+"/templates", nil, t, nil)
}

// DeleteMaintPattern deletes the pattern; an absent one counts as deleted.
func (u UserAPI) DeleteMaintPattern(ctx context.Context, key string) error {
	return absentOK(u.do(ctx, http.MethodDelete, maintPath+"/patterns/"+url.PathEscape(key), nil, nil, nil))
}

// DeleteMaintTemplate deletes the template; an absent one counts as deleted.
func (u UserAPI) DeleteMaintTemplate(ctx context.Context, key string) error {
	return absentOK(u.do(ctx, http.MethodDelete, maintPath+"/templates/"+url.PathEscape(key), nil, nil, nil))
}

// EnableMaint creates the window and its suppression rules.
func (u UserAPI) EnableMaint(ctx context.Context, w MaintWindow) (MaintEnabled, error) {
	var out MaintEnabled
	return out, u.do(ctx, http.MethodPost, maintPath+"/enable", nil, w, &out)
}

// DisableMaint removes the window and its rules; no window counts as disabled.
func (u UserAPI) DisableMaint(ctx context.Context, cr string) error {
	return absentOK(u.do(ctx, http.MethodPost, maintPath+"/disable", nil, map[string]string{"cr_number": cr}, nil))
}

// MaintStatus reads the window state for cr.
func (u UserAPI) MaintStatus(ctx context.Context, cr string) (MaintStatus, error) {
	var out MaintStatus
	return out, u.do(ctx, http.MethodGet, maintPath+"/status/"+url.PathEscape(cr), nil, nil, &out)
}

// MaintRecords lists every ledger row whose CR or description contains q,
// paging through the whole list.
func (u UserAPI) MaintRecords(ctx context.Context, q string) ([]MaintRecord, error) {
	var all []MaintRecord
	for page := 1; ; page++ {
		var out struct {
			Items []MaintRecord `json:"items"`
			Total int           `json:"total"`
		}
		v := url.Values{"q": {q}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		if err := u.do(ctx, http.MethodGet, maintPath, v, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if len(out.Items) == 0 || len(all) >= out.Total {
			return all, nil
		}
	}
}

// MaintLive is the ledger row's live half: present, absent or unavailable.
func (u UserAPI) MaintLive(ctx context.Context, id string) (string, error) {
	var out struct {
		Live string `json:"live_status"`
	}
	return out.Live, u.do(ctx, http.MethodGet, maintPath+"/"+url.PathEscape(id), nil, nil, &out)
}

func absentOK(err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return nil
	}
	return err
}
