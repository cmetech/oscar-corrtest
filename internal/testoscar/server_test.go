package testoscar

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerScriptsResponsesAndRecordsRequests(t *testing.T) {
	server := New(t)
	server.Enqueue(Response{Status: http.StatusCreated, Header: http.Header{"X-Test": {"yes"}}, Body: `{"id":"rule-1"}`})
	request, err := http.NewRequest(http.MethodPost, server.URL()+"/api/v1/correlation/rules?mode=create", strings.NewReader(`{"name":"rule"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer hidden")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated || response.Header.Get("X-Test") != "yes" {
		t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
	}
	recorded := server.Requests()
	if len(recorded) != 1 || recorded[0].Method != http.MethodPost || recorded[0].Path != "/api/v1/correlation/rules" || recorded[0].Query.Get("mode") != "create" || recorded[0].Body != `{"name":"rule"}` {
		t.Fatalf("requests=%+v", recorded)
	}
	if recorded[0].Header.Get("Authorization") != "[REDACTED]" {
		t.Fatalf("authorization=%q", recorded[0].Header.Get("Authorization"))
	}
}

func TestServerCanBlockDeterministically(t *testing.T) {
	server := New(t)
	release := make(chan struct{})
	server.Enqueue(Response{Status: http.StatusOK, Body: "done", Release: release})
	finished := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL() + "/blocked")
		if err == nil {
			_ = response.Body.Close()
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("request finished before release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
}

func TestServerHonorsRequestCancellationWhileBlocked(t *testing.T) {
	server := New(t)
	server.Enqueue(Response{Status: http.StatusOK, Release: make(chan struct{})})
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL()+"/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := http.DefaultClient.Do(request)
		finished <- err
	}()
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled request error=nil")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request remained blocked")
	}
}
