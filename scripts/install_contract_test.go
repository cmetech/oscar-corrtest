package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestInstallersPrintManagedEnvironmentAndExplicitServiceCommands(t *testing.T) {
	posix, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"POSIX": string(posix), "Windows": string(windows)} {
		for _, required := range []string{".env", "service install", "service start", "did not start"} {
			if !strings.Contains(source, required) {
				t.Errorf("%s installer missing %q", name, required)
			}
		}
	}
	for _, forbidden := range []string{"service install\n' |", "service start\n' |"} {
		if strings.Contains(string(posix), forbidden) || strings.Contains(string(windows), forbidden) {
			t.Errorf("installer appears to execute service command marker %q", forbidden)
		}
	}
}

func TestInstallerDefaultLocationsMatchUserContract(t *testing.T) {
	posix, _ := os.ReadFile("install.sh")
	windows, _ := os.ReadFile("install.ps1")
	if !strings.Contains(string(posix), "XDG_BIN_HOME") || !strings.Contains(string(posix), "$HOME/.config") || !strings.Contains(string(posix), "config_root") {
		t.Errorf("POSIX installer lacks XDG binary or managed environment location")
	}
	if !strings.Contains(string(windows), "Programs\\oscar-corrtest") || !strings.Contains(string(windows), "oscar-corrtest\\.env") {
		t.Errorf("Windows installer lacks user program or managed environment location")
	}
}
