package command

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/cmetech/oscar-corrtest/internal/config"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

type compactObservationRuntime interface {
	ObserveCompact(context.Context, *testpack.Kit, *testpack.Installation, string, string) (appruntime.CompactObservationResult, error)
}

func (a *App) observeCompact(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("test-pack observe-compact", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	manifest := flags.String("manifest", "", "original kit manifest.json")
	pin := flags.String("manifest-sha256", "", "separately trusted manifest SHA256")
	installation := flags.String("installation", "", "original installation evidence directory")
	ownership := flags.String("ownership", "", "original ownership declaration")
	apiBase := flags.String("api-base", "", "exact installed API base")
	target := flags.String("target", "", "stored API target ID")
	evidence := flags.String("evidence", "", "new private observation file")
	output := flags.String("output", "human", "human or json")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() != 0 || *manifest == "" || filepath.Base(*manifest) != "manifest.json" ||
		*pin == "" || *installation == "" || *ownership == "" || *apiBase == "" ||
		!installedTargetID(*target) || *evidence == "" || !validOutput(*output) {
		return a.commandUsageError("test-pack observe-compact", "original artifacts, stored API target and a new evidence path are required")
	}
	kit, err := testpack.Load(filepath.Dir(*manifest), *pin)
	if err != nil {
		fmt.Fprintln(a.stderr, "compact observation kit validation unavailable")
		return 3
	}
	owner, err := readInstalledOwnership(*ownership)
	if err != nil {
		fmt.Fprintln(a.stderr, "compact observation ownership validation unavailable")
		return 3
	}
	original, err := testpack.LoadInstallation(*installation, kit, *apiBase, owner)
	if err != nil {
		fmt.Fprintln(a.stderr, "compact observation installation validation unavailable")
		return 3
	}
	application, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer application.Close()
	observer, ok := application.(compactObservationRuntime)
	if !ok {
		fmt.Fprintln(a.stderr, "compact observation runtime unavailable")
		return 3
	}
	result, observeErr := observer.ObserveCompact(ctx, kit, original, *target, *evidence)
	if observeErr == nil && (result.Outcome != "observed" || result.Path == "" || result.Bytes <= 0 || len(result.SHA256) != 64) {
		observeErr = fmt.Errorf("compact observation result incomplete")
	}
	if result.Path != "" {
		if *output == "json" {
			if code := a.writeJSON(result); code != 0 {
				return code
			}
		} else {
			fmt.Fprintf(a.stdout, "Compact observation: %s\nEvidence: %s\nSHA256: %s\nRead-only observation; not scenario PASS.\n",
				result.Outcome, result.Path, result.SHA256)
		}
	}
	if observeErr != nil {
		// Target responses and credential errors never reach terminal output.
		fmt.Fprintln(a.stderr, "compact observation unavailable; inspect retained evidence if a path was reported")
		return 3
	}
	return 0
}
