// Package config resolves local oscar-corrtest configuration without loading secrets.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

const apiVersion = "corrtest.oscar/v1alpha1"

// Settings contains the effective local application settings.
type Settings struct {
	ConfigPath    string
	DataDir       string
	ListenAddress string
}

// Overrides contains command-line values, which have highest precedence.
type Overrides struct {
	ConfigPath    string
	DataDir       string
	ListenAddress string
}

type fileConfig struct {
	APIVersion    string `json:"apiVersion"`
	DataDir       string `json:"dataDir,omitempty"`
	ListenAddress string `json:"listenAddress,omitempty"`
}

// Load applies command, environment, file, and default configuration in order.
func Load(getenv func(string) string, overrides Overrides) (Settings, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	configPath := overrides.ConfigPath
	if configPath == "" {
		configPath = defaultConfigPath(getenv)
	}
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return Settings{}, fmt.Errorf("resolve config path: %w", err)
	}

	settings := Settings{
		ConfigPath:    filepath.Clean(absConfigPath),
		DataDir:       defaultDataDir(getenv),
		ListenAddress: "0.0.0.0:8787",
	}
	fileValues, err := readFile(settings.ConfigPath)
	if err != nil {
		return Settings{}, err
	}
	if fileValues != nil {
		if fileValues.DataDir != "" {
			settings.DataDir = fileValues.DataDir
		}
		if fileValues.ListenAddress != "" {
			settings.ListenAddress = fileValues.ListenAddress
		}
	}
	if value := getenv("OSCAR_CORRTEST_DATA_DIR"); value != "" {
		settings.DataDir = value
	}
	if value := getenv("OSCAR_CORRTEST_LISTEN"); value != "" {
		settings.ListenAddress = value
	}
	if overrides.DataDir != "" {
		settings.DataDir = overrides.DataDir
	}
	if overrides.ListenAddress != "" {
		settings.ListenAddress = overrides.ListenAddress
	}
	if !filepath.IsAbs(settings.DataDir) {
		return Settings{}, fmt.Errorf("data directory %q must be absolute", settings.DataDir)
	}
	settings.DataDir = filepath.Clean(settings.DataDir)
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return Settings{}, fmt.Errorf("invalid listen address %q: %w", settings.ListenAddress, err)
	}
	return settings, nil
}

func readFile(path string) (*fileConfig, error) {
	// #nosec G304 -- reading an explicitly selected configuration path is the intended CLI contract.
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var values fileConfig
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode config: trailing JSON document")
		}
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if values.APIVersion != apiVersion {
		return nil, fmt.Errorf("unsupported config apiVersion %q", values.APIVersion)
	}
	return &values, nil
}

func defaultConfigPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(base, "oscar-corrtest", "config.json")
}

func defaultDataDir(getenv func(string) string) string {
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "oscar-corrtest")
}
