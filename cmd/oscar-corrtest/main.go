package main

import (
	"context"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	info := version.Current()
	paths, err := platformpaths.Resolve(runtime.GOOS, os.LookupEnv)
	if err != nil {
		_, _ = os.Stderr.WriteString("oscar-corrtest: resolve user paths: " + err.Error() + "\n")
		os.Exit(1)
	}
	environment, err := envfile.Open(paths.EnvFile, os.LookupEnv)
	if err != nil {
		_, _ = os.Stderr.WriteString("oscar-corrtest: open managed environment: " + err.Error() + "\n")
		os.Exit(1)
	}
	logs, logErr := applog.Open(paths.LogDir, os.Stderr, applog.Options{})
	if logErr != nil {
		_, _ = os.Stderr.WriteString("oscar-corrtest: structured log unavailable; using stderr only\n")
		logs = applog.StderrOnly(os.Stderr)
	}
	logger := logs.Logger("main")
	serviceFactory := func() (service.Manager, error) {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, executableErr
		}
		return service.NewManager(service.Options{GOOS: runtime.GOOS, Executable: executable, Paths: paths, Runner: service.ExecRunner{}, Stdout: os.Stdout, Stderr: os.Stderr})
	}
	open := func(ctx context.Context, settings config.Settings) (command.ApplicationRuntime, error) {
		manager, managerErr := serviceFactory()
		if managerErr != nil {
			return nil, managerErr
		}
		controller := operations.New(settings, environment, manager, logs)
		return appruntime.OpenWithOptions(ctx, settings, info, appruntime.Options{Environment: environment, Logs: logs, Logger: logs.Logger("runtime"), Operations: controller})
	}
	app := command.NewApplication(os.Stdout, os.Stderr, info, command.Dependencies{Serve: web.Run, Open: open, Getenv: environment.Getenv, Service: serviceFactory, Logger: logger})
	exitCode := app.Run(ctx, os.Args[1:])
	_ = logs.Close()
	os.Exit(exitCode)
}
