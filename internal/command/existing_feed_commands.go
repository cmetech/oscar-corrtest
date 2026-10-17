package command

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runner"
)

type ownedExistingFeedRuntime interface {
	ExecuteOwnedExistingFeed(context.Context, runner.OwnedExistingFeedPlan) (domain.Run, error)
}

func (a *App) runOwnedExistingFeed(ctx context.Context, args []string) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "run") {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest owned-existing-feed <plan|run> --manifest <file>")
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet("owned-existing-feed "+action, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	manifestPath := flags.String("manifest", "", "finite owned-lab plan JSON file")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *manifestPath == "" || !validOutput(*output) {
		fmt.Fprintln(a.stderr, "owned-existing-feed requires --manifest and human/json output")
		return 2
	}
	// #nosec G304 -- this explicit CLI option selects the owned fixture plan file.
	file, err := os.Open(*manifestPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "owned-existing-feed: %v\n", err)
		return 3
	}
	raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	_ = file.Close()
	if err != nil || len(raw) > 2<<20 {
		fmt.Fprintln(a.stderr, "owned-existing-feed plan is unreadable or exceeds 2 MiB")
		return 3
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan runner.OwnedExistingFeedPlan
	if err = decoder.Decode(&plan); err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = fmt.Errorf("trailing JSON is not permitted")
		}
	}
	if err == nil {
		err = plan.Validate()
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "owned-existing-feed: %v\n", err)
		return 3
	}
	if action == "plan" {
		if *output == "json" {
			return a.writeJSON(plan)
		}
		fmt.Fprintf(a.stdout, "Owned lab profile: %s\nFixtures: 6 firing identities\nReceiver: %s\nNative: %s\nHistory: %s\n", plan.Manifest.Profile, plan.Manifest.TargetID, plan.NativeTargetID, plan.HistoryTargetID)
		fmt.Fprintln(a.stdout, "Coverage: identity only. External ownership and immutable Mapping installation are required. No source clear or retry; forwarded inputs retain DIRTY cleanup.")
		return 0
	}
	application, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer application.Close()
	execution, ok := application.(ownedExistingFeedRuntime)
	if !ok {
		fmt.Fprintln(a.stderr, "owned existing-feed execution unavailable")
		return 3
	}
	run, executeErr := execution.ExecuteOwnedExistingFeed(ctx, plan)
	if run.ID != "" {
		if *output == "json" {
			if code = a.writeJSON(run); code != 0 {
				return code
			}
		} else {
			fmt.Fprintf(a.stdout, "Run: %s\nIdentity verdict: %s\nCleanup: %s\n", run.ID, run.Verdict, run.CleanupStatus)
			fmt.Fprintln(a.stdout, "Coverage: original Native and public History identity only; correlation/window, delivery receipt and semantic task proof remain unproved. No source clear or retry.")
		}
	}
	if executeErr != nil {
		fmt.Fprintf(a.stderr, "owned-existing-feed: %v\n", executeErr)
		if run.CleanupStatus == domain.CleanupUnknown || run.CleanupStatus == domain.CleanupDirty {
			return 4
		}
		return 3
	}
	return runExitCode(run)
}
