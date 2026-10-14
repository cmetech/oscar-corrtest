package integration

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/command"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func TestStatusCLICannotRecoverLiveUIRuntime(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	settings := config.Settings{DataDir: filepath.Join(directory, "state")}
	owner, err := appruntime.Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	run, err := owner.CreateRun(ctx, "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"runs", "show", run.ID}, {"runs", "list"}} {
		t.Run(strings.Join(args[:2], "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := command.NewApplication(&stdout, &stderr, version.Info{}, command.Dependencies{
				Open: func(ctx context.Context, s config.Settings) (command.ApplicationRuntime, error) {
					return appruntime.Open(ctx, s, version.Info{})
				},
			})
			args = append(args, "--data-dir", settings.DataDir, "--config", filepath.Join(directory, "unused.json"), "--output", "json")
			if code := app.Run(ctx, args); code != 1 || !strings.Contains(stderr.String(), "already in use") || stdout.Len() != 0 {
				t.Fatalf("second CLI: exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
			}
		})
	}
	handler := web.NewHandlerWithData(version.Info{}, owner)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/runs/"+run.ID, nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), run.ID) {
		t.Fatalf("owner UI read failed: %d %s", response.Code, response.Body.String())
	}
	current, err := owner.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.RunQueued {
		t.Fatalf("live UI run changed to %s", current.Status)
	}
}
