package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const outputAPIVersion = "corrtest.oscar/v1alpha1"

func (a *App) runTarget(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest target <add|list>")
		return 2
	}
	switch args[0] {
	case "add":
		return a.runTargetAdd(ctx, args[1:])
	case "list":
		return a.runTargetList(ctx, args[1:])
	default:
		fmt.Fprintf(a.stderr, "unknown target command %q\n", args[0])
		return 2
	}
}

func (a *App) runTargetAdd(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("target add", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	name := flags.String("name", "", "target display name")
	baseURL := flags.String("url", "", "OSCAR base URL")
	apiProfile := flags.String("api-profile", "public-v1", "OSCAR API profile")
	credentialEnv := flags.String("credential-env", "", "environment credential reference")
	credentialFile := flags.String("credential-file", "", "mounted file credential reference")
	credentialSystemd := flags.String("credential-systemd", "", "systemd credential reference")
	caFile := flags.String("ca-file", "", "custom CA file")
	insecure := flags.Bool("insecure", false, "disable TLS verification for this target")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !validOutput(*output) {
		fmt.Fprintln(a.stderr, "target add accepts flags only and --output human|json")
		return 2
	}
	credential := domain.CredentialRef{}
	count := 0
	for _, candidate := range []struct {
		kind  domain.CredentialKind
		value string
	}{{domain.CredentialEnvironment, *credentialEnv}, {domain.CredentialFile, *credentialFile}, {domain.CredentialSystemd, *credentialSystemd}} {
		if candidate.value != "" {
			count++
			credential = domain.CredentialRef{Kind: candidate.kind, Reference: candidate.value}
		}
	}
	if count > 1 {
		fmt.Fprintln(a.stderr, "target add accepts at most one credential reference")
		return 2
	}
	runtime, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer runtime.Close()
	target, err := runtime.CreateTarget(ctx, domain.TargetInput{
		DisplayName: *name, BaseURL: *baseURL, APIProfile: *apiProfile,
		TLS: domain.TLSPolicy{Insecure: *insecure, CAPath: *caFile}, Credential: credential,
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "target add: %v\n", err)
		return 1
	}
	if *output == "json" {
		return a.writeJSON(target)
	}
	fmt.Fprintf(a.stdout, "Target %s added (%s)\n", target.DisplayName, target.ID)
	return 0
}

func (a *App) runTargetList(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("target list", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !validOutput(*output) {
		return 2
	}
	runtime, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer runtime.Close()
	targets, err := runtime.ListTargets(ctx)
	if err != nil {
		fmt.Fprintf(a.stderr, "target list: %v\n", err)
		return 1
	}
	if *output == "json" {
		return a.writeJSON(targets)
	}
	if len(targets) == 0 {
		fmt.Fprintln(a.stdout, "No targets configured")
		return 0
	}
	for _, target := range targets {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", target.ID, target.DisplayName, target.BaseURL)
	}
	return 0
}

func (a *App) runRuns(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest runs <list|show>")
		return 2
	}
	switch args[0] {
	case "list":
		return a.runRunsList(ctx, args[1:])
	case "show":
		return a.runRunsShow(ctx, args[1:])
	default:
		fmt.Fprintf(a.stderr, "unknown runs command %q\n", args[0])
		return 2
	}
}

func (a *App) runRunsList(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("runs list", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	target := flags.String("target", "", "target ID")
	status := flags.String("status", "", "run status")
	verdict := flags.String("verdict", "", "run verdict")
	cleanup := flags.String("cleanup", "", "cleanup status")
	pattern := flags.String("pattern", "", "correlation pattern")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	filter := domain.RunFilter{TargetID: *target, Status: domain.RunStatus(*status), Verdict: domain.Verdict(*verdict), CleanupStatus: domain.CleanupStatus(*cleanup), Pattern: *pattern}
	if flags.NArg() != 0 || !validOutput(*output) || (filter.Status != "" && !filter.Status.Valid()) ||
		(filter.Verdict != "" && !filter.Verdict.Valid()) || (filter.CleanupStatus != "" && !filter.CleanupStatus.Valid()) {
		fmt.Fprintln(a.stderr, "runs list contains an invalid filter or output format")
		return 2
	}
	runtime, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer runtime.Close()
	runs, err := runtime.ListRuns(ctx, filter)
	if err != nil {
		fmt.Fprintf(a.stderr, "runs list: %v\n", err)
		return 1
	}
	if *output == "json" {
		return a.writeJSON(runs)
	}
	if len(runs) == 0 {
		fmt.Fprintln(a.stdout, "No runs found")
		return 0
	}
	for _, run := range runs {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", run.ID, run.Status, displayEmpty(string(run.Verdict)), run.CleanupStatus)
	}
	return 0
}

func (a *App) runRunsShow(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("runs show", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || !validOutput(*output) {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest runs show [--output human|json] <run-id>")
		return 2
	}
	runtime, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer runtime.Close()
	run, err := runtime.GetRun(ctx, flags.Arg(0))
	if err != nil {
		fmt.Fprintf(a.stderr, "runs show: %v\n", err)
		return 1
	}
	if *output == "json" {
		return a.writeJSON(run)
	}
	fmt.Fprintf(a.stdout, "Run: %s\nStatus: %s\nVerdict: %s\nCleanup: %s\n", run.ID, run.Status, displayEmpty(string(run.Verdict)), run.CleanupStatus)
	return 0
}

func (a *App) runBackup(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	configPath, dataDir := commonConfigFlags(flags)
	output := flags.String("output", "", "backup database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *output == "" {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest backup --output <path>")
		return 2
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintf(a.stderr, "backup: %v\n", err)
		return 2
	}
	runtime, code := a.openRuntime(ctx, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir})
	if code != 0 {
		return code
	}
	defer runtime.Close()
	if err := runtime.Backup(ctx, destination); err != nil {
		fmt.Fprintf(a.stderr, "backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.stdout, "Backup written: %s\n", destination)
	return 0
}

func commonConfigFlags(flags *flag.FlagSet) (*string, *string) {
	return flags.String("config", "", "configuration file"), flags.String("data-dir", "", "local state directory")
}

func (a *App) openRuntime(ctx context.Context, overrides config.Overrides) (ApplicationRuntime, int) {
	if a.open == nil {
		fmt.Fprintln(a.stderr, "durable runtime is unavailable")
		return nil, 1
	}
	settings, err := config.Load(a.getenv, overrides)
	if err != nil {
		fmt.Fprintf(a.stderr, "configuration: %v\n", err)
		return nil, 2
	}
	runtime, err := a.open(ctx, settings)
	if err != nil {
		fmt.Fprintf(a.stderr, "open runtime: %v\n", err)
		return nil, 1
	}
	return runtime, 0
}

func (a *App) writeJSON(data any) int {
	encoder := json.NewEncoder(a.stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		APIVersion string `json:"apiVersion"`
		Data       any    `json:"data"`
	}{APIVersion: outputAPIVersion, Data: data}); err != nil {
		fmt.Fprintf(a.stderr, "encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

func validOutput(output string) bool { return output == "human" || output == "json" }

func displayEmpty(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
