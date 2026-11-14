// Package targetops holds the I/O adapters the campaign pre-checks and engine use against the OSCAR target.
package targetops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ProbeResult mirrors oscar-target-probe/v1.
type ProbeResult struct {
	Schema           string             `json:"schema"`
	UTC              string             `json:"utc"`
	OscarVersion     *string            `json:"oscar_version"`
	MetricsRetention *string            `json:"metrics_retention"` // raw VictoriaMetrics flag: unitless means months
	Images           []ProbeImage       `json:"images"`
	Containers       []ProbeContainer   `json:"containers"`
	Flags            map[string]*string `json:"flags"`
	OverridesSHA256  *string            `json:"overrides_sha256"`
	AlertHistory     *ProbeHistory      `json:"alert_history"`
	Errors           map[string]string  `json:"errors"`
}

type ProbeImage struct {
	Container string `json:"container"`
	Image     string `json:"image"`
	Digest    string `json:"digest"`
}

type ProbeContainer struct {
	Name         string `json:"name"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	OOMKilled    bool   `json:"oom_killed"`
	StartedAt    string `json:"started_at"`
}

type ProbeHistory struct {
	Rows      int64 `json:"rows"`
	DataBytes int64 `json:"data_bytes"`
}

// Probe runs the forced-command SSH key. host may be "oscar@oscar.home.arpa" or a bare host (user oscar).
// No remote command is ever passed. The probe exits 2 with valid JSON when a section failed; that is not an error here.
func Probe(ctx context.Context, sshPath, keyFile, host string) (ProbeResult, error) {
	if !strings.Contains(host, "@") {
		host = "oscar@" + host
	}
	cmd := exec.CommandContext(ctx, sshPath, "-i", keyFile, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "--", host) // #nosec G204 -- fixed argv, operator-supplied paths
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	var result ProbeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		if runErr != nil {
			return ProbeResult{}, fmt.Errorf("target probe failed: %w", runErr)
		}
		return ProbeResult{}, fmt.Errorf("target probe returned malformed JSON")
	}
	if result.Schema != "oscar-target-probe/v1" {
		return ProbeResult{}, fmt.Errorf("unexpected probe schema %q", result.Schema)
	}
	return result, nil
}
