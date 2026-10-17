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
	values := historyQueryValues(query.AlertName, formattedTime(query.Start), formattedTime(query.End))
	result := []HistoryRecord{}
	seen, total := 0, -1
	identities := map[string]bool{}
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		status, raw, err := reader.client.do(ctx, http.MethodGet, historyPath, values, nil)
		if err != nil {
			return nil, err
		}
		var response struct {
			Records      []historyWire `json:"records"`
			TotalRecords *int          `json:"total_records"`
			TotalPages   *int          `json:"total_pages"`
			Page         *int          `json:"page"`
			PerPage      *int          `json:"per_page"`
		}
		if status != http.StatusOK || json.Unmarshal(raw, &response) != nil || response.Records == nil || response.TotalRecords == nil || response.TotalPages == nil || response.Page == nil || response.PerPage == nil {
			return nil, fmt.Errorf("owned History response lacks complete list metadata")
		}
		if *response.TotalRecords < 0 || *response.TotalRecords > maxListPages*100 || *response.PerPage != 100 || *response.Page != page || *response.TotalPages != (*response.TotalRecords+99)/100 || (total >= 0 && total != *response.TotalRecords) {
			return nil, fmt.Errorf("owned History pagination is inconsistent or exceeds bound")
		}
		total = *response.TotalRecords
		expected := total - seen
		if expected > 100 {
			expected = 100
		}
		if expected < 0 || len(response.Records) != expected {
			return nil, fmt.Errorf("owned History page is incomplete or ambiguous")
		}
		seen += len(response.Records)
		for _, wire := range response.Records {
			id, ok := wire.ID.(string)
			if !ok || !existingFeedHistoryID.MatchString(id) || identities[id] {
				return nil, fmt.Errorf("owned History record has invalid or repeated UUID4 identity")
			}
			identities[id] = true
			labels := map[string]bool{}
			for _, label := range wire.Labels {
				if labels[label.Label] {
					return nil, fmt.Errorf("owned History has ambiguous repeated labels")
				}
				labels[label.Label] = true
			}
			if wire.AlertName == query.AlertName {
				result = append(result, wire.normalized())
			}
		}
		if seen == total {
			return result, nil
		}
	}
	return nil, paginationLimitError("owned History GET")
}

type existingFeedHistoryReadTransport struct{ base http.RoundTripper }

func (transport existingFeedHistoryReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Path != historyPath {
		return nil, fmt.Errorf("owned History reader permits only the fixed GET")
	}
	return transport.base.RoundTrip(request)
}
