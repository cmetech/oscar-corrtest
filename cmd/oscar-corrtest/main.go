package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/cmetech/oscar-corrtest/internal/command"
	"github.com/cmetech/oscar-corrtest/internal/config"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	info := version.Current()
	open := func(ctx context.Context, settings config.Settings) (command.ApplicationRuntime, error) {
		return appruntime.Open(ctx, settings, info)
	}
	app := command.NewConfigured(os.Stdout, os.Stderr, info, web.Run, open, os.Getenv)
	os.Exit(app.Run(ctx, os.Args[1:]))
}
