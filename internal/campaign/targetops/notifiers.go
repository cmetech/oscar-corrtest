package targetops

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Notifier is the secret-free projection of an OSCAR notifier (config is never decoded).
type Notifier struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"` // "enabled" | "disabled"
}

// listNotifiers pages until every record is seen: the API caps pages at 10 regardless of the requested size.
func listNotifiers(ctx context.Context, c *oscar.Client) ([]Notifier, error) {
	var all []Notifier
	for page := 1; page <= 200; page++ {
		var resp struct {
			Total   int        `json:"total_records"`
			Records []Notifier `json:"records"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}, "column": {"name"}, "order": {"asc"}}
		if err := c.DoJSON(ctx, http.MethodGet, "/api/v1/notifiers", q, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Records...)
		if len(resp.Records) == 0 || len(all) >= resp.Total {
			return all, nil
		}
	}
	return nil, fmt.Errorf("notifier listing exceeded page limit")
}

// ExternalEnabled returns enabled notifiers whose name is not in keep (case-insensitive).
func ExternalEnabled(ctx context.Context, c *oscar.Client, keep []string) ([]Notifier, error) {
	all, err := listNotifiers(ctx, c)
	if err != nil {
		return nil, err
	}
	var out []Notifier
	for _, n := range all {
		if n.Status != "enabled" || contains(keep, n.Name) {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func contains(list []string, name string) bool {
	for _, k := range list {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// SetNotifierStatus enables or disables one notifier and reads it back; a differing readback is an error.
func SetNotifierStatus(ctx context.Context, c *oscar.Client, id string, enabled bool) (Notifier, error) {
	verb, want := "disable", "disabled"
	if enabled {
		verb, want = "enable", "enabled"
	}
	if err := c.DoJSON(ctx, http.MethodPost, "/api/v1/notifiers/"+verb, nil, []string{id}, nil); err != nil {
		return Notifier{}, err
	}
	all, err := listNotifiers(ctx, c)
	if err != nil {
		return Notifier{}, err
	}
	for _, n := range all {
		if n.ID == id {
			if n.Status != want {
				return n, fmt.Errorf("notifier %s readback status %q, want %q", n.Name, n.Status, want)
			}
			return n, nil
		}
	}
	return Notifier{}, fmt.Errorf("notifier %s missing on readback", id)
}

// ListNotifiers returns every notifier on the target, any status.
func ListNotifiers(ctx context.Context, c *oscar.Client) ([]Notifier, error) {
	return listNotifiers(ctx, c)
}
