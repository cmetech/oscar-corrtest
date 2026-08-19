package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/cmetech/oscar-corrtest/internal/command"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := command.New(os.Stdout, os.Stderr, version.Current())
	os.Exit(app.Run(ctx, os.Args[1:]))
}
