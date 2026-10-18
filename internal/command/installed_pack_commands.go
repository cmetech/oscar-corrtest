package command

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

type installedPackRuntime interface {
	ExecuteInstalledPack(context.Context, *testpack.Kit, *testpack.Installation, string, string) (domain.Run, error)
}

const installedPackCoverage = "Coverage: installed initial Flood audit-only with public Active/History identity; not full UAT, physical parent delivery, task completion or recovery proof. No source clear or automatic retry. Forwarded/unknown inputs retain DIRTY/UNKNOWN responsibility."

func (a *App) runInstalledPack(ctx context.Context, args []string) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "run") {
		return a.commandUsageError("test-pack", "test-pack requires plan or run")
	}
	action := args[0]
	flags := flag.NewFlagSet("test-pack "+action, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	manifest := flags.String("manifest", "", "kit manifest.json path")
	pin := flags.String("manifest-sha256", "", "separately trusted manifest SHA256")
	installation := flags.String("installation", "", "original installation directory")
	ownership := flags.String("ownership", "", "original operator ownership declaration JSON file")
	apiBase := flags.String("api-base", "", "exact original API base")
	target := flags.String("target", "", "stored API target ID")
	receiver := flags.String("receiver-target", "", "stored receiver target ID")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *manifest == "" || filepath.Base(*manifest) != "manifest.json" || *pin == "" || *installation == "" || *ownership == "" || *apiBase == "" || !installedTargetID(*target) || !installedTargetID(*receiver) || !validOutput(*output) {
		return a.commandUsageError("test-pack "+action, "test-pack requires the fixed artifact, original binding and target flags with human/json output")
	}
	// All input validation precedes runtime/config/credential resolution. Load
	// captures immutable bytes; later edits to paths cannot replace these objects.
	kit, err := testpack.Load(filepath.Dir(*manifest), *pin)
	if err != nil {
		fmt.Fprintf(a.stderr, "test-pack kit: %v\n", err)
		return 3
	}
	owner, err := readInstalledOwnership(*ownership)
	if err != nil {
		fmt.Fprintf(a.stderr, "test-pack ownership: %v\n", err)
		return 3
	}
	original, err := testpack.LoadInstallation(*installation, kit, *apiBase, owner)
	if err != nil {
		fmt.Fprintf(a.stderr, "test-pack installation: %v\n", err)
		return 3
	}
	if action == "plan" {
		limits := []string{"Offline artifact verification is not scenario PASS or current target readiness.", "Continuous target ownership is operator-declared, not machine-verified exclusion.", "No source clear or automatic retry; forwarded/unknown inputs retain DIRTY/UNKNOWN responsibility.", "Audit-only scope does not prove physical parent delivery, task completion, recovery or full UAT."}
		if *output == "json" {
			return a.writeJSON(struct {
				Format       string                       `json:"format"`
				API          string                       `json:"apiTargetId"`
				Receiver     string                       `json:"receiverTargetId"`
				Kit          testpack.Bindings            `json:"kit"`
				Installation testpack.InstallationBinding `json:"installation"`
				Inputs       int                          `json:"inputs"`
				Scope        string                       `json:"scope"`
				Limits       []string                     `json:"limits"`
			}{"corrtest-installed-pack-plan-v1", *target, *receiver, kit.Bindings(), original.Binding(), kit.Scenario().InputCount(), "initial Flood audit-only", limits})
		}
		b := original.Binding()
		fmt.Fprintf(a.stdout, "Installed test kit: %s\nManifest SHA256: %s\nOriginal installation attempt: %s\nOriginal load: %s\nOriginal rule ID: %d\nAPI base: %s\nAPI target: %s\nReceiver target: %s\nInputs: %d fixed initial firings\n", kit.Bindings().PackID, kit.Bindings().ManifestSHA256, b.AttemptID, b.LoadID, b.RuleID, b.TargetOrigin, *target, *receiver, kit.Scenario().InputCount())
		fmt.Fprintln(a.stdout, installedPackCoverage)
		for _, limit := range limits {
			fmt.Fprintln(a.stdout, limit)
		}
		return 0
	}
	application, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer application.Close()
	execution, ok := application.(installedPackRuntime)
	if !ok {
		fmt.Fprintln(a.stderr, "installed test-pack execution unavailable")
		return 3
	}
	run, executeErr := execution.ExecuteInstalledPack(ctx, kit, original, *target, *receiver)
	if run.ID != "" {
		if *output == "json" {
			if code = a.writeJSON(run); code != 0 {
				if run.CleanupStatus == domain.CleanupDirty || run.CleanupStatus == domain.CleanupUnknown {
					return 4
				}
				return code
			}
		} else {
			fmt.Fprintf(a.stdout, "Run: %s\nAudit scope verdict: %s\nCleanup: %s\n", run.ID, run.Verdict, run.CleanupStatus)
			fmt.Fprintln(a.stdout, installedPackCoverage)
		}
	}
	if executeErr != nil {
		fmt.Fprintf(a.stderr, "test-pack: %v\n", executeErr)
		if run.CleanupStatus == domain.CleanupDirty || run.CleanupStatus == domain.CleanupUnknown {
			return 4
		}
		return 3
	}
	return runExitCode(run)
}
func installedTargetID(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
