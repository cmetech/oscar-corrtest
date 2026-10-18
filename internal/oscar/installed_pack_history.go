package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const installedHistoryMaxBytes = 16 << 20

// InstalledPackHistoryReader performs only bounded public History reads. It
// establishes neither source identity correspondence nor delivery/task success.
type InstalledPackHistoryReader struct{ client *Client }

// Records are normalized for comparison; Pages retain original response bytes,
// including uninterpreted fields. No observation is returned on partial failure.
type InstalledPackHistoryObservation struct {
	Records []HistoryRecord   `json:"records"`
	Pages   []json.RawMessage `json:"pages"`
}

func NewInstalledPackHistoryReader(target domain.Target, options Options) (*InstalledPackHistoryReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed History requires authenticated standard read target with verified TLS")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood History", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	route, err := installedBaseURL(target.BaseURL, "/ext/mw")
	if err != nil {
		return nil, err
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed History credential is empty")
	}
	client.http.Transport = installedHistoryReadTransport{base: client.http.Transport, route: route}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackHistoryReader{client: client}, nil
}

func (reader *InstalledPackHistoryReader) FindHistory(ctx context.Context, query HistoryQuery) (*InstalledPackHistoryObservation, error) {
	pages := []json.RawMessage{}
	rows, err := readBoundedHistory(ctx, reader.client, query, &pages)
	if err != nil {
		return nil, err
	}
	return &InstalledPackHistoryObservation{Records: rows, Pages: pages}, nil
}

func validateInstalledHistoryPage(raw []byte) error {
	// The fixed installed JSON parser rejects duplicate/folded nested keys, bad
	// UTF-8 and excessive depth before permissive wire normalization can hide it.
	if _, err := installedPackObject(raw); err != nil {
		return err
	}
	page, err := installedAuditObject(raw, "records", "total_records", "total_pages", "page", "per_page")
	if err != nil {
		return err
	}
	var rows []json.RawMessage
	if json.Unmarshal(page["records"], &rows) != nil {
		return fmt.Errorf("installed History records are invalid")
	}
	for _, rawRow := range rows {
		fields, err := installedAuditObject(rawRow, "ID", "alertname")
		if err != nil {
			return err
		}
		for _, nested := range []struct{ field, key string }{{"labels", "Label"}, {"annotations", "Annotation"}} {
			if value, present := fields[nested.field]; present {
				var members []json.RawMessage
				if json.Unmarshal(value, &members) != nil || members == nil {
					return fmt.Errorf("installed History nested rows are invalid")
				}
				for _, member := range members {
					pair, err := installedAuditObject(member, nested.key, "Value")
					if err != nil {
						return err
					}
					for _, key := range []string{nested.key, "Value"} {
						var text *string
						if json.Unmarshal(pair[key], &text) != nil || text == nil {
							return fmt.Errorf("installed History nested pair requires strings")
						}
					}
				}
			}
		}
		for field := range fields {
			for _, canonical := range []string{"original_severity", "severity", "startsAt", "ID", "alertname", "fingerprint", "status", "createdAt", "labels", "annotations"} {
				if field != canonical && strings.EqualFold(field, canonical) {
					return fmt.Errorf("installed History has noncanonical row keys")
				}
			}
		}
	}
	return nil
}

type installedHistoryReadTransport struct {
	base  http.RoundTripper
	route installedBase
}

func (transport installedHistoryReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path, err := transport.route.path(request)
	if err != nil {
		return nil, err
	}
	if request.Method != http.MethodGet || path != historyPath {
		return nil, fmt.Errorf("installed History permits only the fixed GET")
	}
	return transport.base.RoundTrip(request)
}
