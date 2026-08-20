package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/cmetech/oscar-corrtest/internal/command"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
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
	open := func(ctx context.Context, settings config.Settings) (command.ApplicationRuntime, error) {
		return appruntime.OpenWithOptions(ctx, settings, info, appruntime.Options{Environment: environment})
	}
	serviceFactory := func() (service.Manager, error) {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, executableErr
		}
		return service.NewManager(service.Options{GOOS: runtime.GOOS, Executable: executable, Paths: paths, Runner: service.ExecRunner{}, Stdout: os.Stdout, Stderr: os.Stderr})
	}
	app := command.NewApplication(os.Stdout, os.Stderr, info, command.Dependencies{Serve: web.Run, Open: open, Getenv: environment.Getenv, Service: serviceFactory})
	os.Exit(app.Run(ctx, os.Args[1:]))
}
