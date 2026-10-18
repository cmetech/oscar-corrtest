package oscar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestInstalledHistoryPageAndAggregateBounds(t *testing.T) {
	for _, mode := range []string{"empty", "maximum pages", "above maximum", "aggregate bytes", "one page bytes"} {
		t.Run(mode, func(t *testing.T) {
			total, wantCalls := 0, 1
			switch mode {
			case "maximum pages":
				total, wantCalls = maxListPages*100, maxListPages
			case "above maximum":
				total = maxListPages*100 + 1
			case "aggregate bytes":
				total, wantCalls = 600, 5
			case "one page bytes":
				total = 1
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				v := installedHistoryPage(page, total)
				if mode == "aggregate bytes" {
					v["opaque"] = strings.Repeat("x", (7 << 19))
				}
				if mode == "one page bytes" {
					v["opaque"] = strings.Repeat("x", maxResponseBytes)
				}
				_ = json.NewEncoder(w).Encode(v)
			}))
			defer server.Close()
			reader, err := NewInstalledPackHistoryReader(nativeObserverTarget(server.URL), Options{Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"})
			if mode == "empty" || mode == "maximum pages" {
				if err != nil || got == nil || len(got.Records) != total || len(got.Pages) != wantCalls {
					t.Fatalf("valid bound failed %v", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("bound exhaustion produced partial success")
			}
			if calls != wantCalls {
				t.Fatalf("unbounded requests %d != %d", calls, wantCalls)
			}
		})
	}
}

func TestInstalledHistoryPublicAuthenticationCannotFallback(t *testing.T) {
	backendCalls := 0
	_, target, calls := installedPublicProxy(t, "/ext/mw", "correct", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		_ = json.NewEncoder(w).Encode(installedHistoryPage(1, 0))
	}))
	reader, err := NewInstalledPackHistoryReader(target, Options{Getenv: func(string) string { return "wrong" }})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reader.FindHistory(context.Background(), HistoryQuery{AlertName: "CorrTestOperationalFlood"}); err == nil || got != nil || backendCalls != 0 || calls.Load() != 1 {
		t.Fatal("authentication bypass or fallback")
	}
}

func TestInstalledHistoryConstructorRefusesOverrides(t *testing.T) {
	for _, mode := range []string{"no identity", "no credential", "empty credential", "insecure", "custom client", "input override", "ordinary binding", "wrong prefix", "trailing prefix", "query", "fragment"} {
		t.Run(mode, func(t *testing.T) {
			target := nativeObserverTarget("https://api.example/ext/mw")
			options := Options{Getenv: func(string) string { return "key" }}
			switch mode {
			case "no identity":
				target.ID = ""
			case "no credential":
				target.Credential = domain.CredentialRef{}
			case "empty credential":
				options.Getenv = func(string) string { return "" }
			case "insecure":
				target.TLS.Insecure = true
			case "custom client":
				options.HTTPClient = &http.Client{}
			case "input override":
				options.InputPath = "alert-engine"
			case "ordinary binding":
				target.OrdinaryWebhook = &domain.OrdinaryWebhookBinding{}
			case "wrong prefix":
				target.BaseURL = "https://api.example/webhookreceiver"
			case "trailing prefix":
				target.BaseURL += "/"
			case "query":
				target.BaseURL += "?"
			case "fragment":
				target.BaseURL += "#"
			}
			if _, err := NewInstalledPackHistoryReader(target, options); err == nil {
				t.Fatal("unsupported installed transport accepted")
			}
		})
	}
}
