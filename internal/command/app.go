package command

import (
	"context"
	"fmt"
	"io"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

// App is the oscar-corrtest command-line application.
type App struct {
	stdout io.Writer
	stderr io.Writer
	info   version.Info
}

// New constructs an application using the supplied output streams and build metadata.
func New(stdout, stderr io.Writer, info version.Info) *App {
	return &App{stdout: stdout, stderr: stderr, info: info}
}

// Run executes a command and returns its process exit code.
func (a *App) Run(_ context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: oscar-corrtest <version|serve>")
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(a.stdout, "oscar-corrtest %s commit=%s built=%s\n", a.info.Version, a.info.Commit, a.info.BuildDate)
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\nusage: oscar-corrtest <version|serve>\n", args[0])
		return 2
	}
}
