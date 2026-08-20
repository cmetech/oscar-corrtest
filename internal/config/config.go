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
	"runtime"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
)

const apiVersion = "corrtest.oscar/v1alpha1"

// Settings contains the effective local application settings.
type Settings struct {
	ConfigPath    string
	DataDir       string
	EnvFile       string
	LogDir        string
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
	return LoadForOS(runtime.GOOS, getenv, overrides)
}

// LoadForOS is Load with an explicit platform for cross-platform contract tests.
func LoadForOS(goos string, getenv func(string) string, overrides Overrides) (Settings, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	paths, err := platformpaths.Resolve(goos, func(key string) (string, bool) {
		value := getenv(key)
		return value, value != ""
	})
	if err != nil {
		return Settings{}, err
	}
	configPath := overrides.ConfigPath
	if configPath == "" {
		configPath = paths.ConfigFile
	}
	absConfigPath := configPath
	if goos != "windows" {
		absConfigPath, err = filepath.Abs(configPath)
		if err != nil {
			return Settings{}, fmt.Errorf("resolve config path: %w", err)
		}
	}

	settings := Settings{
		ConfigPath:    filepath.Clean(absConfigPath),
		DataDir:       paths.StateDir,
		EnvFile:       paths.EnvFile,
		LogDir:        paths.LogDir,
		ListenAddress: "0.0.0.0:8787",
	}
	if goos == "windows" {
		settings.ConfigPath = strings.ReplaceAll(absConfigPath, "/", `\`)
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
	if !isAbsForOS(goos, settings.DataDir) {
		return Settings{}, fmt.Errorf("data directory %q must be absolute", settings.DataDir)
	}
	settings.DataDir = filepath.Clean(settings.DataDir)
	if _, _, err := net.SplitHostPort(settings.ListenAddress); err != nil {
		return Settings{}, fmt.Errorf("invalid listen address %q: %w", settings.ListenAddress, err)
	}
	return settings, nil
}

func isAbsForOS(goos, value string) bool {
	if goos != "windows" {
		return filepath.IsAbs(value)
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/') || strings.HasPrefix(value, `\\`)
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
