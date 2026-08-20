package command

import (
	"context"
	"flag"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/service"
)

func (a *App) runService(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.commandUsageError("service", "missing service action")
	}
	action := args[0]
	switch action {
	case "install", "start", "stop", "restart", "status", "logs", "uninstall":
	default:
		return a.commandUsageError("service", fmt.Sprintf("unknown service action %q", action))
	}
	if a.service == nil {
		fmt.Fprintln(a.stderr, "service management is unavailable")
		return 1
	}
	manager, err := a.service()
	if err != nil {
		fmt.Fprintf(a.stderr, "service: %v\n", err)
		return 1
	}
	remaining := args[1:]
	switch action {
	case "status":
		if len(remaining) != 0 {
			fmt.Fprintln(a.stderr, "service status does not accept arguments")
			return 2
		}
		status, statusErr := manager.Status(ctx)
		if statusErr != nil {
			fmt.Fprintf(a.stderr, "service status: %v\n", statusErr)
			return 1
		}
		fmt.Fprintf(a.stdout, "%s mechanism=%s", status.State, status.Mechanism)
		if status.PID > 0 {
			fmt.Fprintf(a.stdout, " pid=%d", status.PID)
		}
		fmt.Fprintln(a.stdout)
		if status.State == service.StateRunning {
			return 0
		}
		if status.State == service.StateStopped || status.State == service.StateNotInstalled {
			return 3
		}
		return 1
	case "logs":
		flags := flag.NewFlagSet("service logs", flag.ContinueOnError)
		flags.SetOutput(a.stderr)
		lines := flags.Int("lines", 200, "number of recent records")
		noFollow := flags.Bool("no-follow", false, "print recent records and exit")
		if err := flags.Parse(remaining); err != nil {
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(a.stderr, "service logs does not accept positional arguments")
			return 2
		}
		if err := manager.Logs(ctx, *lines, !*noFollow); err != nil {
			fmt.Fprintf(a.stderr, "service logs: %v\n", err)
			return 1
		}
		return 0
	case "install", "start", "stop", "restart", "uninstall":
		if len(remaining) != 0 {
			fmt.Fprintf(a.stderr, "service %s does not accept arguments\n", action)
			return 2
		}
		var actionErr error
		switch action {
		case "install":
			actionErr = manager.Install(ctx)
		case "start":
			actionErr = manager.Start(ctx)
		case "stop":
			actionErr = manager.Stop(ctx)
		case "restart":
			actionErr = manager.Restart(ctx)
		case "uninstall":
			actionErr = manager.Uninstall(ctx)
		}
		if actionErr != nil {
			fmt.Fprintf(a.stderr, "service %s: %v\n", action, actionErr)
			return 1
		}
		fmt.Fprintf(a.stdout, "service %s complete; definition=%s\n", action, manager.DefinitionPath())
		return 0
	}
	return 2
}
