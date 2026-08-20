package platformpaths

import "testing"

func TestResolvePlatformPaths(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		env      map[string]string
		envFile  string
		stateDir string
	}{
		{"linux fallback", "linux", map[string]string{"HOME": "/home/alex"}, "/home/alex/.config/oscar-corrtest/.env", "/home/alex/.local/state/oscar-corrtest"},
		{"darwin xdg", "darwin", map[string]string{"HOME": "/Users/alex", "XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "/state"}, "/cfg/oscar-corrtest/.env", "/state/oscar-corrtest"},
		{"windows local app data", "windows", map[string]string{"LOCALAPPDATA": `C:\Users\alex\AppData\Local`}, `C:\Users\alex\AppData\Local\oscar-corrtest\.env`, `C:\Users\alex\AppData\Local\oscar-corrtest\data`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.goos, mapLookup(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if got.EnvFile != tt.envFile || got.StateDir != tt.stateDir {
				t.Fatalf("paths=%+v, want env=%q state=%q", got, tt.envFile, tt.stateDir)
			}
			if got.ConfigFile == "" || got.LogDir == "" || got.ApplicationLog == "" || got.BootstrapLog == "" {
				t.Fatalf("incomplete paths: %+v", got)
			}
		})
	}
}

func TestResolveRejectsMissingRootsAndUnsupportedOS(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "plan9"} {
		if _, err := Resolve(goos, mapLookup(nil)); err == nil {
			t.Fatalf("Resolve(%q) error=nil", goos)
		}
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
