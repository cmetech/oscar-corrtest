// Package platformpaths resolves oscar-corrtest's per-user files without
// depending on the host OS, which keeps packaging behavior testable.
package platformpaths

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Paths contains every user-owned path used by the application.
type Paths struct {
	ConfigDir         string
	EnvFile           string
	ConfigFile        string
	StateDir          string
	LogDir            string
	ApplicationLog    string
	BootstrapLog      string
	ServiceDefinition string
}

// Resolve returns the platform path contract for goos.
func Resolve(goos string, lookup func(string) (string, bool)) (Paths, error) {
	if lookup == nil {
		return Paths{}, fmt.Errorf("environment lookup is required")
	}
	var configRoot, stateDir, serviceDefinition string
	switch goos {
	case "linux", "darwin":
		home, _ := lookup("HOME")
		if home == "" {
			return Paths{}, fmt.Errorf("HOME is required for user service paths")
		}
		configRoot, _ = lookup("XDG_CONFIG_HOME")
		if configRoot == "" {
			if home == "" {
				return Paths{}, fmt.Errorf("HOME is required when XDG_CONFIG_HOME is unset")
			}
			configRoot = filepath.Join(home, ".config")
		}
		stateRoot, _ := lookup("XDG_STATE_HOME")
		if stateRoot == "" {
			if home == "" {
				return Paths{}, fmt.Errorf("HOME is required when XDG_STATE_HOME is unset")
			}
			stateRoot = filepath.Join(home, ".local", "state")
		}
		configRoot = filepath.Join(configRoot, "oscar-corrtest")
		stateDir = filepath.Join(stateRoot, "oscar-corrtest")
		if goos == "linux" {
			serviceDefinition = filepath.Join(home, ".config", "systemd", "user", "oscar-corrtest.service")
		} else {
			serviceDefinition = filepath.Join(home, "Library", "LaunchAgents", "io.cmetech.oscar-corrtest.plist")
		}
	case "windows":
		root, _ := lookup("LOCALAPPDATA")
		if strings.TrimSpace(root) == "" {
			return Paths{}, fmt.Errorf("LOCALAPPDATA is required on windows")
		}
		configRoot = windowsJoin(root, "oscar-corrtest")
		stateDir = windowsJoin(configRoot, "data")
		serviceDefinition = windowsJoin(configRoot, "oscar-corrtest-task.xml")
	default:
		return Paths{}, fmt.Errorf("unsupported operating system %q", goos)
	}
	join := filepath.Join
	if goos == "windows" {
		join = windowsJoin
	}
	logDir := join(stateDir, "logs")
	return Paths{
		ConfigDir:         configRoot,
		EnvFile:           join(configRoot, ".env"),
		ConfigFile:        join(configRoot, "config.json"),
		StateDir:          stateDir,
		LogDir:            logDir,
		ApplicationLog:    join(logDir, "application.jsonl"),
		BootstrapLog:      join(logDir, "service-bootstrap.log"),
		ServiceDefinition: serviceDefinition,
	}, nil
}

func windowsJoin(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	result := strings.TrimRight(strings.ReplaceAll(parts[0], "/", `\`), `\`)
	for _, part := range parts[1:] {
		part = strings.Trim(strings.ReplaceAll(part, "/", `\`), `\`)
		if part != "" {
			result += `\` + part
		}
	}
	return result
}
