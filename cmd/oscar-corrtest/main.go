package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/command"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
	"github.com/cmetech/oscar-corrtest/internal/operations"
	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	if handled, exitCode := command.HandleHelp(stdout, stderr, args); handled {
		return exitCode
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	info := version.Current()
	paths, err := platformpaths.Resolve(runtime.GOOS, lookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "oscar-corrtest: resolve user paths: %v\n", err)
		return 1
	}
	environment, err := envfile.Open(paths.EnvFile, lookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "oscar-corrtest: open managed environment: %v\n", err)
		return 1
	}
	logs, logErr := applog.Open(paths.LogDir, stderr, applog.Options{})
	if logErr != nil {
		fmt.Fprintln(stderr, "oscar-corrtest: structured log unavailable; using stderr only")
		logs = applog.StderrOnly(stderr)
	}
	logger := logs.Logger("main")
	serviceFactory := func() (service.Manager, error) {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, executableErr
		}
		return service.NewManager(service.Options{GOOS: runtime.GOOS, Executable: executable, Paths: paths, Runner: service.ExecRunner{}, Stdout: stdout, Stderr: stderr})
	}
	open := func(ctx context.Context, settings config.Settings) (command.ApplicationRuntime, error) {
		manager, managerErr := serviceFactory()
		if managerErr != nil {
			return nil, managerErr
		}
		controller := operations.New(settings, environment, manager, logs)
		return appruntime.OpenWithOptions(ctx, settings, info, appruntime.Options{Environment: environment, Logs: logs, Logger: logs.Logger("runtime"), Operations: controller})
	}
	app := command.NewApplication(stdout, stderr, info, command.Dependencies{Serve: web.Run, Open: open, Getenv: environment.Getenv, Service: serviceFactory, Logger: logger})
	exitCode := app.Run(ctx, args)
	_ = logs.Close()
	return exitCode
}
