package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// ExistingFeedHistoryReader exposes only the public History read needed by the
// owned fixture journey. It proves neither delivery nor task completion.
type ExistingFeedHistoryReader struct{ client *Client }

func NewExistingFeedHistoryReader(target domain.Target, options Options) (*ExistingFeedHistoryReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("existing-feed History requires explicit standard read-only target")
	}
	if err := (domain.TargetInput{DisplayName: "owned existing-feed History", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	origin, err := url.Parse(target.BaseURL)
	if err != nil || origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || strings.Contains(target.BaseURL, "#") {
		return nil, fmt.Errorf("existing-feed History requires an origin without path, query or fragment")
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("existing-feed History credential is empty")
	}
	client.http.Transport = existingFeedHistoryReadTransport{client.http.Transport}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ExistingFeedHistoryReader{client}, nil
}
func (reader *ExistingFeedHistoryReader) FindHistory(ctx context.Context, query HistoryQuery) ([]HistoryRecord, error) {
	return readBoundedHistory(ctx, reader.client, query, nil)
}

// The optional raw-page sink is only used by installed History observation.
// Existing-feed retains its original parsing and no aggregate raw-page storage.
func readBoundedHistory(ctx context.Context, client *Client, query HistoryQuery, pages *[]json.RawMessage) ([]HistoryRecord, error) {
	rawBytes := 0
	values := historyQueryValues(query.AlertName, formattedTime(query.Start), formattedTime(query.End))
	result := []HistoryRecord{}
	state := newBoundedHistoryState()
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		status, raw, err := client.do(ctx, http.MethodGet, historyPath, values, nil)
		if err != nil {
			return nil, err
		}
		if pages != nil {
			if len(raw) > installedHistoryMaxBytes-rawBytes {
				return nil, fmt.Errorf("installed History raw observation exceeds bound")
			}
			if err := validateInstalledHistoryPage(raw); err != nil {
				return nil, err
			}
			rawBytes += len(raw)
			*pages = append(*pages, json.RawMessage(raw))
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("owned History response lacks complete list metadata")
		}
		rows, done, err := state.decode(raw, page, query.AlertName)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
		if done {
			return result, nil
		}
	}
	return nil, paginationLimitError("owned History GET")
}

// The original network loop and installed offline decoder share the exact page
// grammar. The optional installed raw validator remains a separate stricter
// boundary; ordinary existing-feed calls retain their previous behavior.
type boundedHistoryState struct {
	seen, total int
	identities  map[string]bool
}

func newBoundedHistoryState() *boundedHistoryState {
	return &boundedHistoryState{total: -1, identities: map[string]bool{}}
}
func (state *boundedHistoryState) decode(raw []byte, page int, alertName string) ([]HistoryRecord, bool, error) {
	result := []HistoryRecord{}
	var response struct {
		Records      []historyWire `json:"records"`
		TotalRecords *int          `json:"total_records"`
		TotalPages   *int          `json:"total_pages"`
		Page         *int          `json:"page"`
		PerPage      *int          `json:"per_page"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Records == nil || response.TotalRecords == nil || response.TotalPages == nil || response.Page == nil || response.PerPage == nil {
		return nil, false, fmt.Errorf("owned History response lacks complete list metadata")
	}
	if *response.TotalRecords < 0 || *response.TotalRecords > maxListPages*100 || *response.PerPage != 100 || *response.Page != page || *response.TotalPages != (*response.TotalRecords+99)/100 || (state.total >= 0 && state.total != *response.TotalRecords) {
		return nil, false, fmt.Errorf("owned History pagination is inconsistent or exceeds bound")
	}
	state.total = *response.TotalRecords
	expected := state.total - state.seen
	if expected > 100 {
		expected = 100
	}
	if expected < 0 || len(response.Records) != expected {
		return nil, false, fmt.Errorf("owned History page is incomplete or ambiguous")
	}
	state.seen += len(response.Records)
	for _, wire := range response.Records {
		id, ok := wire.ID.(string)
		if !ok || !existingFeedHistoryID.MatchString(id) || state.identities[id] {
			return nil, false, fmt.Errorf("owned History record has invalid or repeated UUID4 identity")
		}
		state.identities[id] = true
		labels := map[string]bool{}
		for _, label := range wire.Labels {
			if labels[label.Label] {
				return nil, false, fmt.Errorf("owned History has ambiguous repeated labels")
			}
			labels[label.Label] = true
		}
		if wire.AlertName == alertName {
			result = append(result, wire.normalized())
		}
	}
	if state.seen == state.total {
		return result, true, nil
	}
	return result, false, nil
}

type existingFeedHistoryReadTransport struct{ base http.RoundTripper }

func (transport existingFeedHistoryReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Path != historyPath {
		return nil, fmt.Errorf("owned History reader permits only the fixed GET")
	}
	return transport.base.RoundTrip(request)
}
