package command

import (
	"fmt"
	"io"
	"strings"
)

type helpItem struct {
	name        string
	description string
}

type helpTopic struct {
	usage       string
	description string
	commands    []helpItem
	options     []helpItem
	examples    []string
	notes       []string
}

var helpTopics = map[string]helpTopic{
	"": {
		usage:       "oscar-corrtest <command> [options]",
		description: "Build, run, and preserve evidence for OSCAR alarm-correlation tests.",
		commands: []helpItem{
			{"serve", "Start the web UI and API."},
			{"service", "Install and control the user background service."},
			{"target", "Configure OSCAR API targets."},
			{"doctor", "Check whether a target can run trustworthy tests."},
			{"scenario", "List, validate, and import test scenarios."},
			{"plan", "Preview rules, alerts, timing, and mutation budget."},
			{"run", "Execute a correlation scenario against OSCAR."},
			{"runs", "List, inspect, and delete preserved runs."},
			{"cleanup", "Retry cleanup for a run."},
			{"retention", "Preview or apply local run retention."},
			{"export", "Export a run as an evidence bundle."},
			{"verify-bundle", "Verify an exported evidence bundle."},
			{"backup", "Back up the local SQLite database."},
			{"version", "Print build version information."},
		},
		examples: []string{
			"oscar-corrtest serve",
			"oscar-corrtest service install",
			"oscar-corrtest service start",
			"oscar-corrtest scenario list",
			"oscar-corrtest help <command>",
		},
		notes: []string{"Run 'oscar-corrtest help <command>' or 'oscar-corrtest <command> --help' for command details."},
	},
	"version": {
		usage:       "oscar-corrtest version",
		description: "Print the release version, source commit, and build timestamp.",
		examples:    []string{"oscar-corrtest version"},
	},
	"serve": {
		usage:       "oscar-corrtest serve [options]",
		description: "Start the embedded web UI and API in the foreground. Stop it with Ctrl-C.",
		options: []helpItem{
			{"--listen <address>", "HTTP listen address (configuration default: 0.0.0.0:8787)."},
			{"--config <path>", "Configuration file path."},
			{"--data-dir <path>", "Local state directory containing the SQLite database and artifacts."},
			{"--remote-mode <mode>", "UI authentication mode: bearer or trusted-proxy."},
			{"--auth-token-env <name>", "Environment variable containing the UI bearer token."},
			{"--auth-token-file <path>", "Absolute file containing the UI bearer token."},
			{"--auth-token-systemd <name>", "systemd credential containing the UI bearer token."},
			{"--proxy-header <name>", "Canonical identity header used in trusted-proxy mode."},
			{"--proxy-value <value>", "Required identity value used in trusted-proxy mode."},
			{"--trusted-proxy <CIDRs>", "Comma-separated trusted proxy CIDRs."},
			{"--tls-cert <path>", "TLS certificate file; requires --tls-key."},
			{"--tls-key <path>", "TLS private key file; requires --tls-cert."},
		},
		examples: []string{
			"oscar-corrtest serve",
			"oscar-corrtest serve --listen 127.0.0.1:8787",
			"oscar-corrtest serve --data-dir /var/tmp/corrtest",
		},
		notes: []string{"The OSCAR API key can be saved from the Operations page after the UI starts."},
	},
	"service": {
		usage:       "oscar-corrtest service <action>",
		description: "Install and control oscar-corrtest as a user-scoped background service.",
		commands: []helpItem{
			{"install", "Create the user service definition without starting it."},
			{"start", "Start the installed user service."},
			{"stop", "Stop the user service."},
			{"restart", "Restart the user service."},
			{"status", "Show installation and running state."},
			{"logs", "Stream or print service logs."},
			{"uninstall", "Remove the user service definition."},
		},
		examples: []string{
			"oscar-corrtest service install",
			"oscar-corrtest service start",
			"oscar-corrtest service status",
		},
		notes: []string{"Install and start are separate, explicit operations."},
	},
	"service install": {
		usage:       "oscar-corrtest service install",
		description: "Create a launchd, systemd, or Windows user-service definition. This does not start the service.",
		examples:    []string{"oscar-corrtest service install", "oscar-corrtest service start"},
	},
	"service start": {
		usage:       "oscar-corrtest service start",
		description: "Start the previously installed user background service.",
		examples:    []string{"oscar-corrtest service install", "oscar-corrtest service start", "oscar-corrtest service status"},
		notes:       []string{"Run 'oscar-corrtest service install' once before starting the service."},
	},
	"service stop": {
		usage:       "oscar-corrtest service stop",
		description: "Stop the installed user background service without removing its definition or data.",
		examples:    []string{"oscar-corrtest service stop", "oscar-corrtest service status"},
	},
	"service restart": {
		usage:       "oscar-corrtest service restart",
		description: "Restart the installed user background service after configuration or binary changes.",
		examples:    []string{"oscar-corrtest service restart", "oscar-corrtest service status"},
	},
	"service status": {
		usage:       "oscar-corrtest service status",
		description: "Report whether the user background service is running, stopped, or not installed.",
		examples:    []string{"oscar-corrtest service status"},
		notes:       []string{"Exit status 0 means running; 3 means stopped or not installed."},
	},
	"service logs": {
		usage:       "oscar-corrtest service logs [options]",
		description: "Show recent background-service logs and follow new records by default.",
		options: []helpItem{
			{"--lines <number>", "Number of recent records to show (default 200)."},
			{"--no-follow", "Print recent records and exit instead of following."},
		},
		examples: []string{"oscar-corrtest service logs", "oscar-corrtest service logs --lines 100 --no-follow"},
	},
	"service uninstall": {
		usage:       "oscar-corrtest service uninstall",
		description: "Remove the user background-service definition without deleting corrtest data.",
		examples:    []string{"oscar-corrtest service stop", "oscar-corrtest service uninstall"},
	},
	"target": {
		usage:       "oscar-corrtest target <action>",
		description: "Create and inspect OSCAR API target definitions.",
		commands: []helpItem{
			{"add", "Add an OSCAR target and credential reference."},
			{"list", "List configured targets."},
		},
		examples: []string{"oscar-corrtest target add --name lab --url https://oscar.example --credential-env OSCAR_API_KEY", "oscar-corrtest target list"},
	},
	"target add": {
		usage:       "oscar-corrtest target add [options]",
		description: "Store an OSCAR target. Supply at most one credential reference; external OSCAR APIs use X-API-Key.",
		options: append(commonHelpOptions(),
			helpItem{"--name <name>", "Target display name (required)."},
			helpItem{"--url <URL>", "OSCAR base URL (required)."},
			helpItem{"--api-profile <name>", "OSCAR API contract profile (default public-v1)."},
			helpItem{"--credential-env <name>", "Environment variable containing the API key."},
			helpItem{"--credential-file <path>", "Mounted file containing the API key."},
			helpItem{"--credential-systemd <name>", "systemd credential containing the API key."},
			helpItem{"--ca-file <path>", "Custom CA certificate file."},
			helpItem{"--insecure", "Disable TLS certificate verification for this target."},
			helpItem{"--output <format>", "Output format: human or json (default human)."},
		),
		examples: []string{"export OSCAR_API_KEY='your-key'", "oscar-corrtest target add --name lab --url https://oscar.example --credential-env OSCAR_API_KEY"},
	},
	"target list": {
		usage:       "oscar-corrtest target list [options]",
		description: "List configured OSCAR targets without exposing credential values.",
		options:     append(commonHelpOptions(), helpItem{"--output <format>", "Output format: human or json (default human)."}),
		examples:    []string{"oscar-corrtest target list", "oscar-corrtest target list --output json"},
	},
	"doctor": {
		usage:       "oscar-corrtest doctor --target <id> --pipeline-mode <mode> [options]",
		description: "Verify OSCAR API access, rule validation, reserved-label survival, and requested pipeline compatibility.",
		options: append(commonHelpOptions(),
			helpItem{"--target <id>", "Configured target ID (required)."},
			helpItem{"--pipeline-mode <mode>", pipelineModeValues + " (required)."},
			helpItem{"--output <format>", "Output format: human or json (default human)."},
		),
		examples: []string{"oscar-corrtest target list", "oscar-corrtest doctor --target tgt_lab --pipeline-mode phase_b_dispatch"},
		notes: []string{
			"Exit status 0 means compatible; 2 means incompatible or invalid usage.",
			"Exit status 3 means diagnostic or OSCAR failure; 1 means a local runtime or output failure.",
		},
	},
	"scenario": {
		usage:       "oscar-corrtest scenario <action>",
		description: "Discover built-in scenarios and validate or import custom scenario YAML.",
		commands: []helpItem{
			{"list", "List built-in and imported scenarios."},
			{"validate", "Validate a scenario file without saving it."},
			{"import", "Validate and save a scenario file."},
		},
		examples: []string{"oscar-corrtest scenario list", "oscar-corrtest scenario validate scenario.yaml", "oscar-corrtest scenario import scenario.yaml"},
	},
	"scenario list": {
		usage:       "oscar-corrtest scenario list [options]",
		description: "List built-in patterns and any scenarios imported into the local catalog.",
		options:     append(commonHelpOptions(), helpItem{"--output <format>", "Output format: human or json (default human)."}),
		examples:    []string{"oscar-corrtest scenario list", "oscar-corrtest scenario list --output json"},
	},
	"scenario validate": {
		usage:       "oscar-corrtest scenario validate <file> [options]",
		description: "Parse and validate a custom scenario YAML file without changing local state.",
		options:     []helpItem{{"--output <format>", "Output format: human or json (default human)."}},
		examples:    []string{"oscar-corrtest scenario validate scenario.yaml", "oscar-corrtest scenario validate scenario.yaml --output json"},
		notes:       []string{"Exit status 3 means the source file is unavailable, unsafe, or invalid; 1 means an output failure."},
	},
	"scenario import": {
		usage:       "oscar-corrtest scenario import <file> [options]",
		description: "Validate and save a custom scenario YAML file in the local catalog.",
		options:     append(commonHelpOptions(), helpItem{"--output <format>", "Output format: human or json (default human)."}),
		examples:    []string{"oscar-corrtest scenario import scenario.yaml", "oscar-corrtest scenario list"},
		notes:       []string{"Exit status 3 means the source file is unavailable, unsafe, or invalid; 1 means a runtime or persistence failure."},
	},
	"plan": planOrRunHelp("plan", "Compile and preview a scenario without creating OSCAR rules or injecting alerts."),
	"run":  planOrRunHelp("run", "Execute a scenario, evaluate its assertions, preserve evidence, and clean up owned OSCAR resources."),
	"runs": {
		usage:       "oscar-corrtest runs <action>",
		description: "Query and manage correlation-test runs preserved in local storage.",
		commands: []helpItem{
			{"list", "Filter and list saved runs."},
			{"show", "Show one run's status and verdict."},
			{"delete", "Delete one cleanup-safe run and its local evidence."},
		},
		examples: []string{"oscar-corrtest runs list", "oscar-corrtest runs show <run-id>", "oscar-corrtest runs delete <run-id> --yes"},
	},
	"runs list": {
		usage:       "oscar-corrtest runs list [options]",
		description: "List saved runs, optionally filtering by target, lifecycle, verdict, cleanup state, or pattern.",
		options: append(commonHelpOptions(),
			helpItem{"--target <id>", "Filter by target ID."},
			helpItem{"--status <status>", "Filter by QUEUED, PREFLIGHT, SETTING_UP, INJECTING, OBSERVING, ASSERTING, CANCELLING, CLEANING_UP, INTERRUPTED, RECOVERING, or COMPLETED."},
			helpItem{"--verdict <verdict>", "Filter by PASS, FAIL, INCONCLUSIVE, ERROR, or SKIPPED."},
			helpItem{"--cleanup <status>", "Filter by CLEAN, DIRTY, NOT_REQUIRED, or UNKNOWN."},
			helpItem{"--pattern <pattern>", "Filter by correlation pattern."},
			helpItem{"--output <format>", "Output format: human or json (default human)."},
		),
		examples: []string{"oscar-corrtest runs list", "oscar-corrtest runs list --pattern flood --verdict PASS", "oscar-corrtest runs list --output json"},
	},
	"runs show": {
		usage:       "oscar-corrtest runs show <run-id> [options]",
		description: "Show the stored lifecycle, verdict, and cleanup state for one exact run ID.",
		options:     append(commonHelpOptions(), helpItem{"--output <format>", "Output format: human or json (default human)."}),
		examples:    []string{"oscar-corrtest runs show crt_0123456789", "oscar-corrtest runs show crt_0123456789 --output json"},
	},
	"runs delete": {
		usage:       "oscar-corrtest runs delete <exact-run-id> --yes [options]",
		description: "Delete one exact cleanup-safe terminal run and its verified local evidence.",
		options:     append(commonHelpOptions(), helpItem{"--yes", "Confirm deletion of the exact run ID."}),
		examples:    []string{"oscar-corrtest runs delete crt_0123456789 --yes"},
		notes:       []string{"This never performs prefix deletion and does not delete OSCAR operator resources."},
	},
	"cleanup": {
		usage:       "oscar-corrtest cleanup <action>",
		description: "Reconcile OSCAR resources owned by a saved test run.",
		commands:    []helpItem{{"retry", "Retry cleanup for one exact run ID."}},
		examples:    []string{"oscar-corrtest cleanup retry <run-id>"},
	},
	"cleanup retry": {
		usage:       "oscar-corrtest cleanup retry <run-id> [options]",
		description: "Retry cleanup of OSCAR resources recorded as owned by one run.",
		options:     commonHelpOptions(),
		examples:    []string{"oscar-corrtest cleanup retry crt_0123456789"},
		notes:       []string{"Exit status 4 means cleanup remains dirty or ownership is unknown."},
	},
	"retention": {
		usage:       "oscar-corrtest retention <action>",
		description: "Preview or delete old cleanup-safe terminal runs from local storage.",
		commands: []helpItem{
			{"preview", "Show runs eligible before a cutoff."},
			{"apply", "Delete eligible runs after explicit confirmation."},
		},
		examples: []string{"oscar-corrtest retention preview --before 2026-08-01T00:00:00Z", "oscar-corrtest retention apply --before 2026-08-01T00:00:00Z --yes"},
	},
	"retention preview": retentionHelp("preview"),
	"retention apply":   retentionHelp("apply"),
	"export": {
		usage:       "oscar-corrtest export <run-id> --output <zip-path> [options]",
		description: "Create a new evidence ZIP for one saved run and print its SHA-256 digest.",
		options:     append(commonHelpOptions(), helpItem{"--output <path>", "New evidence ZIP destination (required)."}),
		examples:    []string{"oscar-corrtest export crt_0123456789 --output ./corrtest-evidence.zip"},
	},
	"verify-bundle": {
		usage:       "oscar-corrtest verify-bundle <zip-path> [options]",
		description: "Verify the integrity and manifest of an exported corrtest evidence bundle.",
		options:     commonHelpOptions(),
		examples:    []string{"oscar-corrtest verify-bundle ./corrtest-evidence.zip"},
	},
	"backup": {
		usage:       "oscar-corrtest backup --output <path> [options]",
		description: "Create a consistent backup of the local corrtest SQLite database.",
		options:     append(commonHelpOptions(), helpItem{"--output <path>", "Backup database destination (required)."}),
		examples:    []string{"oscar-corrtest backup --output ./corrtest-backup.db"},
	},
}

func commonHelpOptions() []helpItem {
	return []helpItem{
		{"--config <path>", "Configuration file path."},
		{"--data-dir <path>", "Local state directory."},
	}
}

func planOrRunHelp(name, description string) helpTopic {
	topic := helpTopic{
		usage:       fmt.Sprintf("oscar-corrtest %s <builtin:pattern|scenario-file> --target <id> --pipeline-mode <mode> [options]", name),
		description: description,
		options: append(commonHelpOptions(),
			helpItem{"--target <id>", "Configured target ID (required)."},
			helpItem{"--pipeline-mode <mode>", pipelineModeValues + " (required)."},
			helpItem{"--output <format>", "Output format: human or json (default human)."},
		),
		examples: []string{
			"oscar-corrtest scenario list",
			fmt.Sprintf("oscar-corrtest %s builtin:flood --target tgt_lab --pipeline-mode phase_b_dispatch", name),
			fmt.Sprintf("oscar-corrtest %s scenario.yaml --target tgt_lab --pipeline-mode phase_b_dispatch --output json", name),
		},
		notes: []string{"Use 'plan' before 'run' to inspect the proposed OSCAR mutations and maximum duration."},
	}
	if name == "run" {
		topic.notes = append(topic.notes, "Exit status: 0 PASS/SKIPPED, 1 FAIL, 2 INCONCLUSIVE, 3 execution error, 4 cleanup dirty/unknown.")
	} else {
		topic.notes = append(topic.notes, "Exit status 3 means a source, compilation, or execution-contract error.")
	}
	return topic
}

func retentionHelp(action string) helpTopic {
	usage := fmt.Sprintf("oscar-corrtest retention %s --before <RFC3339> [options]", action)
	description := "List cleanup-safe terminal runs created before the UTC cutoff without deleting them."
	options := append(commonHelpOptions(),
		helpItem{"--before <RFC3339>", "Select runs created before this timestamp (required)."},
		helpItem{"--output <format>", "Output format: human or json (default human)."},
	)
	if action == "apply" {
		usage = "oscar-corrtest retention apply --before <RFC3339> --yes [options]"
		description = "Delete cleanup-safe terminal runs created before the UTC cutoff."
		options = append(options, helpItem{"--yes", "Confirm retention deletion."})
	}
	return helpTopic{
		usage:       usage,
		description: description,
		options:     options,
		examples:    []string{fmt.Sprintf("oscar-corrtest retention %s --before 2026-08-01T00:00:00Z%s", action, map[bool]string{true: " --yes"}[action == "apply"])},
	}
}

func helpRequest(args []string) (string, bool) {
	if len(args) == 0 {
		return "", true
	}
	if args[0] == "help" {
		if len(args) == 1 || (len(args) == 2 && (args[1] == "--help" || args[1] == "-h")) {
			return "", true
		}
		return strings.Join(args[1:], " "), true
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "", true
	}
	last := args[len(args)-1]
	if last == "--help" || last == "-h" {
		return strings.Join(args[:len(args)-1], " "), true
	}
	if len(args) == 2 && args[1] == "help" {
		return args[0], true
	}
	return "", false
}

// HandleHelp renders a help request before process-level configuration,
// logging, runtime, or service initialization occurs.
func HandleHelp(stdout, stderr io.Writer, args []string) (bool, int) {
	path, requested := helpRequest(args)
	if !requested {
		return false, 0
	}
	app := &App{stdout: stdout, stderr: stderr}
	return true, app.runHelp(path)
}

func (a *App) runHelp(path string) int {
	path = strings.Join(strings.Fields(path), " ")
	topic, ok := helpTopics[path]
	if !ok {
		fmt.Fprintf(a.stderr, "unknown help topic %q\n", path)
		parent := ""
		parts := strings.Fields(path)
		if len(parts) > 1 {
			candidate := strings.Join(parts[:len(parts)-1], " ")
			if _, exists := helpTopics[candidate]; exists {
				parent = candidate
			}
		}
		if parent == "" {
			fmt.Fprintln(a.stderr, "Run 'oscar-corrtest --help' to list commands.")
		} else {
			fmt.Fprintf(a.stderr, "Run 'oscar-corrtest %s --help' to list valid actions.\n", parent)
		}
		return 2
	}
	writeHelpTopic(a.stdout, path, topic)
	return 0
}

func (a *App) commandUsageError(path, message string) int {
	fmt.Fprintln(a.stderr, message)
	fmt.Fprintf(a.stderr, "Run 'oscar-corrtest %s --help' for usage.\n", path)
	return 2
}

func writeHelpTopic(output io.Writer, path string, topic helpTopic) {
	if path == "" {
		fmt.Fprintln(output, "OSCAR Correlation Test Harness")
	} else {
		fmt.Fprintf(output, "oscar-corrtest %s\n", path)
	}
	fmt.Fprintf(output, "\nDescription:\n  %s\n", topic.description)
	fmt.Fprintf(output, "\nUsage:\n  %s\n", topic.usage)
	writeHelpItems(output, "Commands", topic.commands)
	writeHelpItems(output, "Options", append(topic.options, helpItem{"-h, --help", "Show help for this command."}))
	if len(topic.examples) > 0 {
		heading := "Examples"
		if path == "" {
			heading = "Getting started"
		}
		fmt.Fprintf(output, "\n%s:\n", heading)
		for _, example := range topic.examples {
			fmt.Fprintf(output, "  %s\n", example)
		}
	}
	if len(topic.notes) > 0 {
		fmt.Fprintln(output, "\nNotes:")
		for _, note := range topic.notes {
			fmt.Fprintf(output, "  %s\n", note)
		}
	}
}

func writeHelpItems(output io.Writer, heading string, items []helpItem) {
	if len(items) == 0 {
		return
	}
	width := 0
	for _, item := range items {
		if len(item.name) > width {
			width = len(item.name)
		}
	}
	fmt.Fprintf(output, "\n%s:\n", heading)
	for _, item := range items {
		fmt.Fprintf(output, "  %-*s  %s\n", width, item.name, item.description)
	}
}
