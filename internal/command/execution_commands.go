package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

type correlationRuntime interface {
	PreviewBuiltin(context.Context, string, string, string) (compiler.Plan, error)
	ExecuteBuiltin(context.Context, string, string, string, bool) (domain.Run, error)
}

type customCorrelationRuntime interface {
	PreviewScenario(context.Context, string, scenario.Scenario, string) (compiler.Plan, error)
	ExecuteScenario(context.Context, string, scenario.Scenario, string, bool) (domain.Run, error)
}

func (a *App) runScenario(_ context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest scenario <list|validate>")
		return 2
	}
	if args[0] == "validate" {
		return a.runScenarioValidate(args[1:])
	}
	if args[0] != "list" {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest scenario <list|validate>")
		return 2
	}
	flags := flag.NewFlagSet("scenario list", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !validOutput(*output) {
		return 2
	}
	type summary struct {
		Name      string `json:"name"`
		Pattern   string `json:"pattern"`
		CaseCount int    `json:"caseCount"`
	}
	var values []summary
	for _, item := range scenario.AllBuiltins() {
		values = append(values, summary{Name: item.Name, Pattern: item.Pattern, CaseCount: len(item.Cases)})
	}
	if *output == "json" {
		return a.writeJSON(values)
	}
	for _, item := range values {
		fmt.Fprintf(a.stdout, "%s\t%s\t%d cases\n", item.Pattern, item.Name, item.CaseCount)
	}
	return 0
}

func (a *App) runScenarioValidate(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest scenario validate <file> [--output human|json]")
		return 2
	}
	path := args[0]
	flags := flag.NewFlagSet("scenario validate", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !validOutput(*output) {
		return 2
	}
	// #nosec G304 -- the command intentionally validates an operator-selected scenario file.
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(a.stderr, "scenario validate: %v\n", err)
		return 3
	}
	defer file.Close()
	document, err := scenario.Decode(file)
	if err != nil {
		fmt.Fprintf(a.stderr, "scenario validate: %v\n", err)
		return 3
	}
	result := struct {
		Valid   bool   `json:"valid"`
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
	}{true, document.Name, document.Pattern}
	if *output == "json" {
		return a.writeJSON(result)
	}
	fmt.Fprintf(a.stdout, "Valid scenario: %s (%s)\n", document.Name, document.Pattern)
	return 0
}

func (a *App) runPlan(ctx context.Context, args []string) int {
	return a.runPlanOrExecute(ctx, args, false)
}

func (a *App) runCorrelation(ctx context.Context, args []string) int {
	return a.runPlanOrExecute(ctx, args, true)
}

func (a *App) runPlanOrExecute(ctx context.Context, args []string, execute bool) int {
	name := "plan"
	if execute {
		name = "run"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	target := flags.String("target", "", "target ID")
	mode := flags.String("pipeline-mode", "", "publication_disabled, phase_a_audit_only, phase_b_dispatch, or unknown")
	labelsSurvived := flags.Bool("labels-survived", false, "declare a successful reserved-label round trip")
	output := flags.String("output", "human", "human or json")
	source := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		source, parseArgs = args[0], args[1:]
	}
	if err := flags.Parse(parseArgs); err != nil {
		return 2
	}
	if source == "" && flags.NArg() == 1 {
		source = flags.Arg(0)
	}
	if flags.NArg() > 1 || source == "" || *target == "" || !validOutput(*output) {
		fmt.Fprintf(a.stderr, "usage: oscar-corrtest %s <builtin:pattern|scenario-file> --target <id> --pipeline-mode <mode> [--labels-survived]\n", name)
		return 2
	}
	builtin := strings.HasPrefix(source, "builtin:")
	pattern := strings.TrimPrefix(source, "builtin:")
	var custom scenario.Scenario
	if !builtin {
		// #nosec G304 -- plan/run intentionally consumes an operator-selected scenario file.
		file, err := os.Open(source)
		if err != nil {
			fmt.Fprintf(a.stderr, "%s: %v\n", name, err)
			return 3
		}
		custom, err = scenario.Decode(file)
		_ = file.Close()
		if err != nil {
			fmt.Fprintf(a.stderr, "%s: %v\n", name, err)
			return 3
		}
		pattern = custom.Pattern
	}
	if !validPipelineMode(*mode) {
		fmt.Fprintln(a.stderr, "pipeline mode must be publication_disabled, phase_a_audit_only, phase_b_dispatch, or unknown")
		return 2
	}
	application, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer application.Close()
	execution, ok := application.(correlationRuntime)
	if !ok {
		fmt.Fprintln(a.stderr, "correlation execution is unavailable")
		return 3
	}
	if !execute {
		var plan compiler.Plan
		var err error
		if builtin {
			plan, err = execution.PreviewBuiltin(ctx, *target, pattern, *mode)
		} else if customExecution, ok := application.(customCorrelationRuntime); ok {
			plan, err = customExecution.PreviewScenario(ctx, *target, custom, *mode)
		} else {
			err = fmt.Errorf("custom scenario execution is unavailable")
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "plan: %v\n", err)
			return 3
		}
		if *output == "json" {
			return a.writeJSON(plan)
		}
		fmt.Fprintf(a.stdout, "Pattern: %s\nRules: %d\nAlerts: %d\nMaximum duration: %s\n", plan.Pattern, plan.MutationBudget.Rules, plan.MutationBudget.Alerts, plan.MaxDuration)
		return 0
	}
	var run domain.Run
	var err error
	if builtin {
		run, err = execution.ExecuteBuiltin(ctx, *target, pattern, *mode, *labelsSurvived)
	} else if customExecution, ok := application.(customCorrelationRuntime); ok {
		run, err = customExecution.ExecuteScenario(ctx, *target, custom, *mode, *labelsSurvived)
	} else {
		err = fmt.Errorf("custom scenario execution is unavailable")
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "run: %v\n", err)
		return 3
	}
	if *output == "json" {
		if code := a.writeJSON(run); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(a.stdout, "Run: %s\nVerdict: %s\nCleanup: %s\n", run.ID, run.Verdict, run.CleanupStatus)
	}
	return runExitCode(run)
}

func validPipelineMode(mode string) bool {
	switch mode {
	case "publication_disabled", "phase_a_audit_only", "phase_b_dispatch", "unknown":
		return true
	default:
		return false
	}
}

func runExitCode(run domain.Run) int {
	if run.CleanupStatus == domain.CleanupDirty || run.CleanupStatus == domain.CleanupUnknown {
		return 4
	}
	switch run.Verdict {
	case domain.VerdictPass, domain.VerdictSkipped:
		return 0
	case domain.VerdictFail:
		return 1
	case domain.VerdictInconclusive:
		return 2
	default:
		return 3
	}
}
