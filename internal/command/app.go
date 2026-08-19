package command

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"

	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

// ServeFunc starts the embedded web application.
type ServeFunc func(context.Context, web.Options) error

// App is the oscar-corrtest command-line application.
type App struct {
	stdout io.Writer
	stderr io.Writer
	info   version.Info
	serve  ServeFunc
}

// New constructs an application using the supplied output streams and build metadata.
func New(stdout, stderr io.Writer, info version.Info, serve ServeFunc) *App {
	return &App{stdout: stdout, stderr: stderr, info: info, serve: serve}
}

// Run executes a command and returns its process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest <version|serve>")
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(a.stdout, "oscar-corrtest %s commit=%s built=%s\n", a.info.Version, a.info.Commit, a.info.BuildDate)
		return 0
	case "serve":
		return a.runServe(ctx, args[1:])
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\nusage: oscar-corrtest <version|serve>\n", args[0])
		return 2
	}
}

func (a *App) runServe(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	listen := flags.String("listen", "127.0.0.1:8787", "literal loopback listen address")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(a.stderr, "serve does not accept positional arguments")
		return 2
	}
	if err := validateLoopback(*listen); err != nil {
		fmt.Fprintf(a.stderr, "%v; authenticated remote serving is not implemented\n", err)
		return 2
	}
	if a.serve == nil {
		fmt.Fprintln(a.stderr, "serve is unavailable")
		return 1
	}
	fmt.Fprintf(a.stdout, "listening on http://%s\n", *listen)
	if err := a.serve(ctx, web.Options{ListenAddress: *listen, Version: a.info}); err != nil {
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
