package targetops

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func testClient(t *testing.T, h http.Handler) *oscar.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := oscar.New(domain.Target{ID: "t", APIProfile: "public-v1", BaseURL: srv.URL + "/ext/mw"},
		oscar.Options{HTTPClient: srv.Client(), Getenv: func(string) string { return "k" }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
