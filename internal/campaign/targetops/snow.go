package targetops

import (
	"slices"

	"bytes"
	"context"
	"encoding/json"
	"fmt"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SNOW talks directly to a ServiceNow PDI Table API with its own basic-auth credential.
type SNOW struct {
	base, user, pass string
	hc               *http.Client
	WakeInterval     time.Duration
}

type Comment struct {
	Value     string `json:"value"`
	CreatedOn string `json:"sys_created_on"`
}

type Incident struct {
	Number           string
	SysID            string
	State            string
	ShortDescription string
	Comments         []Comment
}

// NewSNOW reads {"username","password"} from credFile. caFile may be empty (public CA).
func NewSNOW(baseURL, credFile, caFile string) (*SNOW, error) {
	raw, err := readSecret(credFile)
	if err != nil {
		return nil, err
	}
	var cred struct{ Username, Password string }
	if json.Unmarshal([]byte(raw), &cred) != nil || cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("SNOW credential file must hold username and password")
	}
	hc, err := newHTTP(caFile, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return &SNOW{base: strings.TrimRight(baseURL, "/"), user: cred.Username, pass: cred.Password, hc: hc, WakeInterval: 10 * time.Second}, nil
}

// call returns the "result" member; a non-JSON 200 (PDI hibernation page) is an error.
func (s *SNOW) call(ctx context.Context, method, path string, q url.Values, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := s.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	set := func(r *http.Request) {
		r.SetBasicAuth(s.user, s.pass)
		r.Header.Set("Accept", "application/json")
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	status, raw, err := doRaw(ctx, s.hc, method, u, rd, set)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("SNOW %s %s returned HTTP %d", method, path, status)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil, fmt.Errorf("SNOW %s %s returned non-JSON (instance may be hibernating)", method, path)
	}
	return env.Result, nil
}

// Wake polls a trivial read until the PDI answers JSON or max elapses.
func (s *SNOW) Wake(ctx context.Context, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		_, err := s.call(ctx, http.MethodGet, "/api/now/table/incident", url.Values{"sysparm_limit": {"1"}, "sysparm_fields": {"sys_id"}}, nil)
		if err == nil {
			return nil
		}
		if !time.Now().Add(s.WakeInterval).Before(deadline) {
			return fmt.Errorf("SNOW did not wake within %s: %w", max, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.WakeInterval):
		}
	}
}

type incidentWire struct {
	Number           string `json:"number"`
	SysID            string `json:"sys_id"`
	State            string `json:"state"`
	ShortDescription string `json:"short_description"`
}

func (w incidentWire) inc() Incident {
	return Incident{Number: w.Number, SysID: w.SysID, State: w.State, ShortDescription: w.ShortDescription}
}

const incidentFields = "number,sys_id,state,short_description"

func validNumber(n string) bool {
	return n != "" && !strings.ContainsAny(n, "^=&? ")
}

// Incident fetches one incident and its comments journal (sys_journal_field, element=comments).
func (s *SNOW) Incident(ctx context.Context, number string) (Incident, error) {
	if !validNumber(number) {
		return Incident{}, fmt.Errorf("invalid incident number")
	}
	res, err := s.call(ctx, http.MethodGet, "/api/now/table/incident", url.Values{
		"sysparm_query": {"number=" + number}, "sysparm_limit": {"1"}, "sysparm_fields": {incidentFields}}, nil)
	if err != nil {
		return Incident{}, err
	}
	var rows []incidentWire
	if json.Unmarshal(res, &rows) != nil || len(rows) != 1 {
		return Incident{}, fmt.Errorf("incident %s not found", number)
	}
	inc := rows[0].inc()
	jr, err := s.call(ctx, http.MethodGet, "/api/now/table/sys_journal_field", url.Values{
		"sysparm_query":  {"element_id=" + inc.SysID + "^element=comments^ORDERBYsys_created_on"},
		"sysparm_fields": {"value,sys_created_on"}}, nil)
	if err != nil {
		return Incident{}, err
	}
	if json.Unmarshal(jr, &inc.Comments) != nil {
		return Incident{}, fmt.Errorf("malformed journal response")
	}
	return inc, nil
}

// Resolve PATCHes state=6 and reads back; the result is an error unless the state is resolved (6) or closed (7).
func (s *SNOW) Resolve(ctx context.Context, number, note string) (Incident, error) {
	inc, err := s.Incident(ctx, number)
	if err != nil {
		return Incident{}, err
	}
	if _, err := s.call(ctx, http.MethodPatch, "/api/now/table/incident/"+inc.SysID, nil,
		map[string]string{"state": "6", "close_code": "Resolved by caller", "close_notes": note}); err != nil {
		return Incident{}, err
	}
	after, err := s.Incident(ctx, number)
	if err != nil {
		return Incident{}, err
	}
	if after.State != "6" && after.State != "7" {
		return after, fmt.Errorf("incident %s readback state %q, want 6 or 7", number, after.State)
	}
	return after, nil
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)

// OpenCampaignIncidents lists unresolved incidents (state not 6, 7, 8) whose short_description starts with
// "Campaign-<campaignRun>-" (OSCAR uses the alert summary, which the lane sender sets to the alertname).
// Rows are re-checked client-side so a prefix-colliding run id never leaks in.
func (s *SNOW) OpenCampaignIncidents(ctx context.Context, campaignRun string) ([]Incident, error) {
	if !runIDPattern.MatchString(campaignRun) {
		return nil, fmt.Errorf("invalid campaign run id")
	}
	return s.openIncidentsWithPrefix(ctx, "Campaign-"+campaignRun+"-")
}

// shortTokenPattern is a run's 8-character Crockford short token (domain.RunID.Short).
var shortTokenPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{8}$`)

// OpenCorrTestIncidents lists open incidents for one correlation run's
// source alerts: short descriptions starting with the compiler's exact
// "[CORRTEST][<pattern>][TRIGGER|NON-TRIGGER][<TOKEN>]" prefix.
func (s *SNOW) OpenCorrTestIncidents(ctx context.Context, pattern, shortToken string) ([]Incident, error) {
	token := strings.ToUpper(shortToken)
	if !slices.Contains(corr.SupportedPatterns(), pattern) {
		return nil, fmt.Errorf("unknown correlation pattern %q", pattern)
	}
	if !shortTokenPattern.MatchString(token) {
		return nil, fmt.Errorf("invalid run short token")
	}
	var out []Incident
	for _, kind := range []string{"TRIGGER", "NON-TRIGGER"} {
		incs, err := s.openIncidentsWithPrefix(ctx, "[CORRTEST]["+pattern+"]["+kind+"]["+token+"]")
		if err != nil {
			return nil, err
		}
		out = append(out, incs...)
	}
	return out, nil
}

// openIncidentsWithPrefix pages open incidents whose short description
// starts with prefix, rechecking the prefix client-side.
func (s *SNOW) openIncidentsWithPrefix(ctx context.Context, prefix string) ([]Incident, error) {
	const pageSize, maxPages = 500, 20 // hard cap 10,000 rows: error rather than silently truncate
	out := []Incident{}
	for page := 0; page < maxPages; page++ {
		res, err := s.call(ctx, http.MethodGet, "/api/now/table/incident", url.Values{
			"sysparm_query":  {"short_descriptionSTARTSWITH" + prefix + "^stateNOT IN6,7,8^ORDERBYsys_id"},
			"sysparm_limit":  {strconv.Itoa(pageSize)},
			"sysparm_offset": {strconv.Itoa(page * pageSize)},
			"sysparm_fields": {incidentFields}}, nil)
		if err != nil {
			return nil, err
		}
		var rows []incidentWire
		if json.Unmarshal(res, &rows) != nil {
			return nil, fmt.Errorf("malformed incident list")
		}
		for _, r := range rows {
			if strings.HasPrefix(r.ShortDescription, prefix) {
				out = append(out, r.inc())
			}
		}
		if len(rows) < pageSize {
			return out, nil
		}
	}
	return nil, fmt.Errorf("incident listing exceeded %d rows", pageSize*maxPages)
}
