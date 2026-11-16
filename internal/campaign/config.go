// Package campaign holds the campaign configuration shared by the CLI and service.
package campaign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Config is the campaign configuration file. Credential fields are file paths;
// secrets never appear in the file. MetricsCredentialFile is optional.
type Config struct {
	TargetID                                 string
	WorkerURL, WorkerTokenFile, WorkerCAFile string
	ProbeHost, ProbeKeyFile                  string
	SinkURL, SinkAdminTokenFile, SinkCAFile  string
	MetricsURL, MetricsCAFile                string
	MetricsCredentialFile                    string
	SNOWInstanceURL, SNOWCredentialFile      string
	FrontOfficeDir                           string
	CampaignNotifier                         string // "CAMPAIGN_SINK"
}

// LoadConfig reads a strict JSON config; unknown fields are rejected.
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-selected --campaign-config path is the CLI contract.
	if err != nil {
		return Config{}, fmt.Errorf("read campaign config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parse campaign config: %w", err)
	}
	if dec.More() {
		return Config{}, fmt.Errorf("parse campaign config: trailing data")
	}
	return c, nil
}
