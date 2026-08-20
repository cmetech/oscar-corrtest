package command

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

// ServeFunc starts the embedded web application.
type ServeFunc func(context.Context, web.Options) error

// ApplicationRuntime is the secret-safe durable service surface used by CLI commands.
type ApplicationRuntime interface {
	CreateTarget(context.Context, domain.TargetInput) (domain.Target, error)
	ListTargets(context.Context) ([]domain.Target, error)
	ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error)
	GetRun(context.Context, string) (domain.Run, error)
	ListRunEvents(context.Context, string) ([]domain.RunEvent, error)
	ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error)
	ReadyStatus() (bool, string)
	Backup(context.Context, string) error
	Close() error
}

// OpenRuntimeFunc initializes durable services from effective configuration.
type OpenRuntimeFunc func(context.Context, config.Settings) (ApplicationRuntime, error)

// App is the oscar-corrtest command-line application.
type App struct {
	stdout io.Writer
	stderr io.Writer
	info   version.Info
	serve  ServeFunc
	open   OpenRuntimeFunc
	getenv func(string) string
}

// New constructs an application using the supplied output streams and build metadata.
func New(stdout, stderr io.Writer, info version.Info, serve ServeFunc) *App {
	return &App{stdout: stdout, stderr: stderr, info: info, serve: serve, getenv: os.Getenv}
}

// NewConfigured constructs the complete CLI with durable runtime initialization.
func NewConfigured(stdout, stderr io.Writer, info version.Info, serve ServeFunc, open OpenRuntimeFunc, getenv func(string) string) *App {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &App{stdout: stdout, stderr: stderr, info: info, serve: serve, open: open, getenv: getenv}
}

// Run executes a command and returns its process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest <version|serve|target|scenario|plan|run|runs|export|verify-bundle|backup>")
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(a.stdout, "oscar-corrtest %s commit=%s built=%s\n", a.info.Version, a.info.Commit, a.info.BuildDate)
		return 0
	case "serve":
		return a.runServe(ctx, args[1:])
	case "target":
		return a.runTarget(ctx, args[1:])
	case "runs":
		return a.runRuns(ctx, args[1:])
	case "backup":
		return a.runBackup(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "verify-bundle":
		return a.runVerifyBundle(ctx, args[1:])
	case "scenario":
		return a.runScenario(ctx, args[1:])
	case "plan":
		return a.runPlan(ctx, args[1:])
	case "run":
		return a.runCorrelation(ctx, args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(a.stdout, "usage: oscar-corrtest <version|serve|target|scenario|plan|run|runs|export|verify-bundle|backup>")
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\nusage: oscar-corrtest <version|serve|target|scenario|plan|run|runs|export|verify-bundle|backup>\n", args[0])
		return 2
	}
}

func (a *App) runServe(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	listen := flags.String("listen", "", "literal loopback listen address")
	configPath := flags.String("config", "", "configuration file")
	dataDir := flags.String("data-dir", "", "local state directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "serve does not accept positional arguments")
		return 2
	}
	settings, err := config.Load(a.getenv, config.Overrides{ConfigPath: *configPath, DataDir: *dataDir, ListenAddress: *listen})
	if err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 2
	}
	if err := validateLoopback(settings.ListenAddress); err != nil {
		fmt.Fprintf(a.stderr, "%v; authenticated remote serving is not implemented\n", err)
		return 2
	}
	if a.serve == nil {
		fmt.Fprintln(a.stderr, "serve is unavailable")
		return 1
	}
	var application ApplicationRuntime
	if a.open != nil {
		application, err = a.open(ctx, settings)
		if err != nil {
			fmt.Fprintf(a.stderr, "serve: %v\n", err)
			return 1
		}
		defer application.Close()
	}
	fmt.Fprintf(a.stdout, "listening on http://%s\n", settings.ListenAddress)
	if err := a.serve(ctx, web.Options{ListenAddress: settings.ListenAddress, Version: a.info, Data: application}); err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

func validateLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q must use a literal loopback IP", address)
	}
	return nil
}
