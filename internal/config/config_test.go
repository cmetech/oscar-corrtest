package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesXDGDefaults(t *testing.T) {
	env := map[string]string{
		"HOME":            "/home/corrtest",
		"XDG_CONFIG_HOME": "/tmp/config-home",
		"XDG_STATE_HOME":  "/tmp/state-home",
	}
	got, err := Load(mapEnv(env), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != "/tmp/config-home/oscar-corrtest/config.json" {
		t.Fatalf("ConfigPath=%q", got.ConfigPath)
	}
	if got.DataDir != "/tmp/state-home/oscar-corrtest" {
		t.Fatalf("DataDir=%q", got.DataDir)
	}
	if got.ListenAddress != "127.0.0.1:8787" {
		t.Fatalf("ListenAddress=%q", got.ListenAddress)
	}
}

func TestLoadAppliesFileEnvironmentAndCLIOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := `{"apiVersion":"corrtest.oscar/v1alpha1","dataDir":"/from-file","listenAddress":"127.0.0.1:8001"}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME":                    dir,
		"OSCAR_CORRTEST_DATA_DIR": "/from-env",
		"OSCAR_CORRTEST_LISTEN":   "127.0.0.1:8002",
	}
	got, err := Load(mapEnv(env), Overrides{
		ConfigPath:    path,
		DataDir:       "/from-cli",
		ListenAddress: "127.0.0.1:8003",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDir != "/from-cli" || got.ListenAddress != "127.0.0.1:8003" {
		t.Fatalf("settings=%+v", got)
	}

	got, err = Load(mapEnv(env), Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDir != "/from-env" || got.ListenAddress != "127.0.0.1:8002" {
		t.Fatalf("settings=%+v", got)
	}

	got, err = Load(mapEnv(map[string]string{"HOME": dir}), Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDir != "/from-file" || got.ListenAddress != "127.0.0.1:8001" {
		t.Fatalf("settings=%+v", got)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"wrong version", `{"apiVersion":"v2"}`},
		{"unknown field", `{"apiVersion":"corrtest.oscar/v1alpha1","secret":"no"}`},
		{"trailing document", `{"apiVersion":"corrtest.oscar/v1alpha1"}{}`},
		{"relative data directory", `{"apiVersion":"corrtest.oscar/v1alpha1","dataDir":"relative"}`},
		{"invalid listener", `{"apiVersion":"corrtest.oscar/v1alpha1","listenAddress":"bad"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(mapEnv(map[string]string{"HOME": t.TempDir()}), Overrides{ConfigPath: path}); err == nil {
				t.Fatal("Load() error=nil")
			}
		})
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
