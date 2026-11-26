package targetops

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Receivers is the receivers lane's OSCAR surface (OSC-207): Mappings through
// the middleware mapping API (no permission beyond a valid key; the
// middleware clears the receivers' caches on each write), probes to the
// webhook profile route, the trap port and the SMTP plaintext port, and the
// lane's History rows.
type Receivers struct {
	C         *oscar.Client
	Root      string // https://<target host>
	HTTP      *http.Client
	Community string // CORRTEST_SNMP_COMMUNITY; never logged
}

const mappingPath = "/api/v1/mapping-data/mapping"

type mappingWire struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Namespace string                     `json:"mapping_namespace_name"`
	Elements  []receivers.MappingElement `json:"element"`
}

func (r Receivers) find(ctx context.Context, ns, name string) (*mappingWire, error) {
	var list []mappingWire
	if err := r.C.DoJSON(ctx, http.MethodGet, mappingPath, url.Values{"name": {name}}, nil, &list); err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name && strings.EqualFold(list[i].Namespace, ns) {
			return &list[i], nil
		}
	}
	return nil, nil
}

// Mapping returns a Mapping's elements, or found=false.
func (r Receivers) Mapping(ctx context.Context, ns, name string) ([]receivers.MappingElement, bool, error) {
	m, err := r.find(ctx, ns, name)
	if err != nil || m == nil {
		return nil, false, err
	}
	return m.Elements, true, nil
}

// MappingNames lists a namespace's Mapping names with prefix.
func (r Receivers) MappingNames(ctx context.Context, ns, prefix string) ([]string, error) {
	var page struct {
		Rows []mappingWire `json:"rows"`
	}
	if err := r.C.DoJSON(ctx, http.MethodGet, mappingPath, url.Values{"mapping_namespace_name": {ns}, "limit": {"500"}}, nil, &page); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range page.Rows {
		if strings.HasPrefix(m.Name, prefix) && strings.EqualFold(m.Namespace, ns) {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

// CreateMapping creates a Mapping.
func (r Receivers) CreateMapping(ctx context.Context, ns, name, description string, els []receivers.MappingElement) error {
	return r.C.DoJSON(ctx, http.MethodPost, mappingPath, nil,
		map[string]any{"name": name, "mapping_namespace_name": ns, "description": description, "element": els}, nil)
}

// DeleteMapping deletes a Mapping by name; an absent one is already deleted.
func (r Receivers) DeleteMapping(ctx context.Context, ns, name string) error {
	m, err := r.find(ctx, ns, name)
	if err != nil || m == nil {
		return err
	}
	return r.C.DoJSON(ctx, http.MethodDelete, mappingPath+"/"+url.PathEscape(m.ID), nil, nil, nil)
}

func (r Receivers) host() string {
	u, _ := url.Parse(r.Root)
	return u.Hostname()
}

// Send sends one probe through receiver (receivers.Names); profile is the
// campaign webhook profile.
func (r Receivers) Send(ctx context.Context, receiver, profile string, p receivers.Probe) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	switch receiver {
	case "webhook":
		return receivers.SendWebhook(ctx, r.HTTP, r.Root+receivers.WebhookPath+profile, p, time.Now())
	case "trap":
		if r.Community == "" {
			return fmt.Errorf("CORRTEST_SNMP_COMMUNITY is not set")
		}
		return receivers.SendTrap(ctx, net.JoinHostPort(r.host(), receivers.TrapPort), r.Community, p)
	case "smtp":
		return receivers.SendSMTP(ctx, net.JoinHostPort(r.host(), receivers.SMTPPort), p)
	}
	return fmt.Errorf("unknown receiver %q", receiver)
}

// ProbeHistory reads the receivers lane's History rows (every campaign's;
// the caller matches its own probe names).
func (r Receivers) ProbeHistory(ctx context.Context, start, end time.Time) ([]oscar.HistoryRecord, error) {
	return r.C.HistoryByLabel(ctx, "campaign_lane", receivers.Lane, start, end)
}

// Reachable is pre-check B17's transport half: the webhook receiver itself
// answers the profile route (405 to a GET: the route is POST-only, so a
// reverse proxy's 404 or 502 means it is not reached), the SMTP plaintext
// port answers 220, EHLO and QUIT, and the trap community is configured. UDP
// cannot be checked without a trap.
func (r Receivers) Reachable(ctx context.Context) []string {
	var problems []string
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.Root+receivers.WebhookPath+"ct-rx-precheck", nil)
	if resp, err := r.HTTP.Do(req); err != nil {
		problems = append(problems, "webhook receiver unreachable: "+err.Error())
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			problems = append(problems, fmt.Sprintf("webhook receiver profile route answers HTTP %d to a GET, want 405 from the receiver", resp.StatusCode))
		}
	}
	if err := smtpHello(ctx, net.JoinHostPort(r.host(), receivers.SMTPPort)); err != nil {
		problems = append(problems, "SMTP receiver: "+err.Error())
	}
	if r.Community == "" {
		problems = append(problems, "trap: CORRTEST_SNMP_COMMUNITY is not set in the controller's environment")
	}
	return problems
}

func smtpHello(ctx context.Context, addr string) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	br := bufio.NewReader(c)
	reply := func() (string, error) {
		var last string
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return "", err
			}
			last = line
			if len(line) < 4 || line[3] != '-' {
				return last, nil
			}
		}
	}
	for _, step := range []struct{ send, want string }{{"", "220"}, {"EHLO corrtest.example.com\r\n", "250"}, {"QUIT\r\n", "221"}} {
		if step.send != "" {
			if _, err := c.Write([]byte(step.send)); err != nil {
				return err
			}
		}
		line, err := reply()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, step.want) {
			return fmt.Errorf("answered %q, want %s", strings.TrimSpace(line), step.want)
		}
	}
	return nil
}

// ActiveProbes lists the receivers-lane probes native Alertmanager holds
// active (OSCAR /api/v1/alerts/active-alerts, source alertmanager_native),
// for run, or for every campaign when run is "". The filter is applied by
// the server (pages are positional over a changing active set, so a
// client-side scan of every page could miss a probe); a read that does not
// return total_records rows is an error, never "none left".
func (r Receivers) ActiveProbes(ctx context.Context, run string) ([]receivers.ActiveAlert, error) {
	items := []map[string]string{{"field": "label:campaign_lane", "operator": "equals", "value": receivers.Lane}}
	if run != "" {
		items = append(items, map[string]string{"field": "label:campaign_run", "operator": "equals", "value": run})
	}
	filter, _ := json.Marshal(map[string]any{"items": items, "logicOperator": "and"})
	var out []receivers.ActiveAlert
	seen := 0
	for page := 1; page <= 200; page++ {
		var resp struct {
			Total      int    `json:"total_records"`
			TotalPages int    `json:"total_pages"`
			Source     string `json:"source"`
			Records    []struct {
				Labels []struct{ Name, Value string } `json:"alert_labels"`
				Ref    struct {
					StartsAt time.Time `json:"starts_at"`
				} `json:"fingerprint_sample_reference"`
				StartsAt time.Time `json:"starts_at"`
			} `json:"records"`
		}
		if err := r.C.DoJSON(ctx, http.MethodGet, "/api/v1/alerts/active-alerts", url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}, "filter": {string(filter)}}, nil, &resp); err != nil {
			return nil, err
		}
		seen += len(resp.Records)
		if resp.Source != "" && resp.Source != "alertmanager_native" {
			return nil, fmt.Errorf("active alerts come from %q, not native Alertmanager", resp.Source)
		}
		for _, rec := range resp.Records {
			labels := map[string]string{}
			for _, l := range rec.Labels {
				labels[l.Name] = l.Value
			}
			if labels["campaign_lane"] != receivers.Lane || run != "" && labels["campaign_run"] != run {
				continue
			}
			start := rec.Ref.StartsAt
			if start.IsZero() {
				start = rec.StartsAt
			}
			out = append(out, receivers.ActiveAlert{Labels: labels, StartsAt: start})
		}
		if page >= resp.TotalPages || len(resp.Records) == 0 {
			if seen != resp.Total {
				return nil, fmt.Errorf("active alerts read %d of %d rows (the set changed during paging)", seen, resp.Total)
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("active alerts exceeded 200 pages")
}

// ResolveNative resolves active probes through native Alertmanager's
// ingest, in one request.
func (r Receivers) ResolveNative(ctx context.Context, alerts []receivers.ActiveAlert) error {
	if len(alerts) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return receivers.ResolveNative(ctx, r.HTTP, r.Root+receivers.IngestPath, alerts, time.Now())
}

// CommunityFromEnv is the trap community the controller's environment holds.
func CommunityFromEnv(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return strings.TrimSpace(getenv("CORRTEST_SNMP_COMMUNITY"))
}
