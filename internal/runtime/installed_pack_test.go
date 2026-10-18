//go:build linux || darwin

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

// Retarget the original controlled installer fixture in private copies. No live
// installation is claimed; the actual loader still verifies its entire journal.
func installedRuntimeObjects(t *testing.T, base string) (*testpack.Kit, *testpack.Installation) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"kit", "original", "original/kit"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	copyFiles := func(from, to string) {
		entries, err := os.ReadDir(from)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(from, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(to, entry.Name()), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyFiles("../testpack/testdata/kit", filepath.Join(dir, "kit"))
	copyFiles("../testpack/testdata/kit", filepath.Join(dir, "original/kit"))
	copyFiles("../testpack/testdata/installation/records", filepath.Join(dir, "original"))
	var owner []byte
	for _, name := range []string{"attempt.json", "completed.json"} {
		path := filepath.Join(dir, "original", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err = json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		object["target"], _ = json.Marshal(base)
		var declaration map[string]json.RawMessage
		if err = json.Unmarshal(object["ownership_declaration"], &declaration); err != nil {
			t.Fatal(err)
		}
		declaration["target"], _ = json.Marshal(base)
		owner, _ = json.Marshal(declaration)
		object["ownership_declaration"] = owner
		raw, _ = json.Marshal(object)
		raw, err = testpack.CanonicalJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	kit, err := testpack.Load(filepath.Join(dir, "kit"), "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := testpack.LoadInstallation(filepath.Join(dir, "original"), kit, base, owner)
	if err != nil {
		t.Fatal(err)
	}
	return kit, installation
}

func TestInstalledRuntimeCloseCancelsAndJoinsPreflight(t *testing.T) {
	entered := make(chan struct{}, 1)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			http.Error(w, "mutation forbidden", 500)
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	kit, installation := installedRuntimeObjects(t, server.URL)
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	app, err := Open(ctx, settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.getenv = func(string) string { return "test-key" }
	var targets []domain.Target
	for _, name := range []string{"api", "receiver"} {
		target, e := app.CreateTarget(ctx, domain.TargetInput{DisplayName: name, BaseURL: server.URL, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "TEST_KEY"}})
		if e != nil {
			t.Fatal(e)
		}
		targets = append(targets, target)
	}
	done := make(chan error, 1)
	go func() {
		_, e := app.ExecuteInstalledPack(ctx, kit, installation, targets[0].ID, targets[1].ID)
		done <- e
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("did not reach preflight: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("preflight did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- app.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to cancel/join installed execution")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preflight succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close returned before execution joined")
	}
	if posts.Load() != 0 {
		t.Fatal("preflight cancellation sent an input")
	}
	if _, err = app.ExecuteInstalledPack(ctx, kit, installation, targets[0].ID, targets[1].ID); err == nil {
		t.Fatal("closed runtime admitted installed run")
	}
	reopened, err := Open(ctx, settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runs, err := reopened.ListRuns(ctx, domain.RunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("retained runs=%d err=%v", len(runs), err)
	}
	if runs[0].CleanupStatus != domain.CleanupNotRequired {
		t.Fatalf("zero-input responsibility=%s", runs[0].CleanupStatus)
	}
}

func TestInstalledRuntimeInvalidObjectsCreateNoRun(t *testing.T) {
	ctx := context.Background()
	app, err := Open(ctx, config.Settings{DataDir: t.TempDir()}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err = app.ExecuteInstalledPack(ctx, nil, nil, "missing-api", "missing-receiver"); err == nil {
		t.Fatal("invalid objects accepted")
	}
	runs, err := app.ListRuns(ctx, domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("invalid input created run: %v %v", runs, err)
	}
}

func TestInstalledRuntimeCloseRetainsQueuedModeWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "queued cancellation must not contact target", 500)
	}))
	defer server.Close()
	kit, installation := installedRuntimeObjects(t, server.URL)
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	app, err := Open(ctx, settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.getenv = func(string) string { return "test-key" }
	target, err := app.CreateTarget(ctx, domain.TargetInput{DisplayName: "owned", BaseURL: server.URL, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "TEST_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	app.runMu.Lock()
	locked := true
	defer func() {
		if locked {
			app.runMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { _, e := app.ExecuteInstalledPack(ctx, kit, installation, target.ID, target.ID); done <- e }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, e := app.ListRuns(ctx, domain.RunFilter{})
		if e != nil {
			t.Fatal(e)
		}
		if len(runs) == 1 {
			if runs[0].InputPath() != "installed-test-pack" {
				t.Fatalf("queued installed run lost its mode before execution: %+v", runs[0])
			}
			break
		}
		select {
		case e := <-done:
			t.Fatalf("execution never queued: %v", e)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan error, 1)
	go func() { closed <- app.Close() }()
	for {
		app.activeMu.Lock()
		closing := app.closing
		app.activeMu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("close did not start")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("Close released a queued execution")
	default:
	}
	app.runMu.Unlock()
	locked = false
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued execution did not join")
	}
	if e := <-done; e == nil {
		t.Fatal("cancelled queued execution succeeded")
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled queued execution contacted target")
	}
	reopened, err := Open(ctx, settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runs, err := reopened.ListRuns(ctx, domain.RunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("run lost: %v %v", runs, err)
	}
	if runs[0].InputPath() != "installed-test-pack" || runs[0].CleanupStatus != domain.CleanupNotRequired {
		t.Fatalf("queued mode/responsibility lost: %+v", runs[0])
	}
}
