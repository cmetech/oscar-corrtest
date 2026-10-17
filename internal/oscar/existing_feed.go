package oscar

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ExistingFeedMappingSnapshot retains exact fixture bytes, not a live Mapping lookup.
type ExistingFeedMappingSnapshot struct {
	Source     string `json:"source"`
	SHA256     string `json:"sha256"`
	BodyBase64 string `json:"bodyBase64"`
}

// ExistingFeedManifestSpec is an owned-lab assertion, not proof of endpoint ownership.
// Persist this value and revalidate it with FreezeExistingFeedManifest on reopen.
type ExistingFeedManifestSpec struct {
	CohortID       string                      `json:"cohortId"`
	TargetID       string                      `json:"targetId"`
	ReceiverOrigin string                      `json:"receiverOrigin"`
	Profile        string                      `json:"profile"`
	ProfileHash    string                      `json:"profileHash"`
	Mapping        ExistingFeedMappingSnapshot `json:"mapping"`
	UserAgent      string                      `json:"userAgent"`
	RequestHost    string                      `json:"requestHost"`
	StartsAt       time.Time                   `json:"startsAt"`
}

// ExistingFeedManifest has no mutable exported state and does not send requests.
type ExistingFeedManifest struct{ spec ExistingFeedManifestSpec }

type ExistingFeedIdentity struct {
	Labels   map[string]string `json:"labels"`
	StartsAt time.Time         `json:"startsAt"`
}

type ExistingFeedNativeObservation struct {
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type ExistingFeedRequestEvidence struct {
	Manifest     ExistingFeedManifestSpec `json:"manifest"`
	FixtureIndex int                      `json:"fixtureIndex"`
	Request      InjectionRequestEvidence `json:"request"`
	Expected     ExistingFeedIdentity     `json:"expected"`
}

func FreezeExistingFeedManifest(spec ExistingFeedManifestSpec) (ExistingFeedManifest, error) {
	profileHash, mappingHash, mappingSource := "", "", ""
	switch spec.Profile {
	case "splunk":
		profileHash = "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b"
		mappingHash = "11a7c673edb751f5c0ce3524457977b503a39ee36979b51ce9a1c6a1f5213eae"
		mappingSource = "tests/oscar-webhookreceiver/fixtures/splunk/baseline.json"
	case "middleware_custom_alert":
		profileHash = "faff8119b0665b4d15b7fcce6c7bc65f160f092af83c928d2c5d2d35a0c8b51e"
		mappingHash = "bfcac60131c132c22385138f7346fbcd4d38dc1b96746429570ba24907deaffd"
		mappingSource = "tests/oscar-webhookreceiver/fixtures/middleware_custom_alert/EXT_CUSTOM_ALERTS.json"
	default:
		return ExistingFeedManifest{}, fmt.Errorf("unsupported existing-feed fixture profile")
	}
	if spec.ProfileHash != profileHash || spec.Mapping.SHA256 != mappingHash || spec.Mapping.Source != mappingSource {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed profile or Mapping provenance differs from qualified fixture")
	}
	if len(spec.Mapping.BodyBase64) > base64.StdEncoding.EncodedLen(maxRecordedInjectionBytes) {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed Mapping snapshot exceeds 1 MiB")
	}
	snapshot, err := base64.StdEncoding.DecodeString(spec.Mapping.BodyBase64)
	sum := sha256.Sum256(snapshot)
	if err != nil || hex.EncodeToString(sum[:]) != mappingHash {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed Mapping snapshot absent or hash mismatch")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(spec.CohortID) || strings.TrimSpace(spec.TargetID) == "" {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed requires owned cohort and target identity")
	}
	_, offset := spec.StartsAt.Zone()
	if spec.StartsAt.IsZero() || spec.StartsAt.Unix() <= 0 || spec.StartsAt.Nanosecond() != 0 || offset != 0 || spec.StartsAt.Year() > 9999 {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed requires explicit whole-second UTC start")
	}
	if spec.UserAgent != "owned-existing-feed/1" || spec.RequestHost != "owned-receiver" {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed requires qualified normalization User-Agent and Host")
	}
	origin, err := url.Parse(spec.ReceiverOrigin)
	if err != nil || origin.Hostname() == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.Path != "" || origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || strings.Contains(spec.ReceiverOrigin, "#") {
		return ExistingFeedManifest{}, fmt.Errorf("existing-feed requires credential-free HTTP(S) receiver origin without path, query or fragment")
	}
	return ExistingFeedManifest{spec: spec}, nil
}

// Prepare builds only the six qualified firing inputs; it never infers clear or order.
func (m ExistingFeedManifest) Prepare(index int) (ExistingFeedRequestEvidence, []byte, error) {
	if m.spec.Profile == "" || index < 0 || index > 5 {
		return ExistingFeedRequestEvidence{}, nil, fmt.Errorf("existing-feed requires frozen manifest and fixture index 0..5")
	}
	prefix := "owned-existing-" + m.spec.CohortID + "-"
	host := prefix + strconv.Itoa(index)
	name := "OrdinaryWindowProof"
	if index == 5 {
		name = "OrdinaryWindowNoMatch"
	}
	site := "NYC-" + m.spec.Profile
	started := m.spec.StartsAt.Format("2006-01-02T15:04:05-07:00")
	var payload map[string]any
	var labels map[string]string
	// shortcut: only the two pinned six-input fixtures are modeled; qualify new payloads before expanding.
	if m.spec.Profile == "splunk" {
		payload = map[string]any{"search_name": name, "sid": prefix, "result": map[string]string{"host": host, "severity": "critical", "site": site, "_time": strconv.FormatInt(m.spec.StartsAt.Unix(), 10)}}
		labels = map[string]string{"alertname": name, "node": host, "severity": "critical", "source": host, "category": "webhook_splunk", "sid": prefix, "site": site, "user_agent": m.spec.UserAgent, "mapped": "true", "summary_copy": name + " on " + host + " (critical)"}
	} else {
		payload = map[string]any{"type": "alternate", "alertname": name, "hostname": host, "site": site, "event_timestamp": started}
		labels = map[string]string{"alertname": name, "hostname": host, "site": site, "event_timestamp": started, "severity": "warning", "category": "alternate", "alert_type": "alternate", "application": "alternate", "instance": host}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ExistingFeedRequestEvidence{}, nil, err
	}
	sum := sha256.Sum256(body)
	request := InjectionRequestEvidence{Format: "corrtest-existing-feed-request-v1", TargetID: m.spec.TargetID, APIProfile: m.spec.Profile, Method: http.MethodPost, Endpoint: m.spec.ReceiverOrigin + "/api/v1/webhookreceiver/webhook/" + m.spec.Profile, ContentType: "application/json", BodyBytes: len(body), BodySHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body), ProfilePrecondition: `"` + m.spec.ProfileHash + `"`}
	return ExistingFeedRequestEvidence{Manifest: m.spec, FixtureIndex: index, Request: request, Expected: ExistingFeedIdentity{Labels: labels, StartsAt: m.spec.StartsAt}}, body, nil
}

// VerifyNative requires exactly one full original Native identity, not enriched History labels.
// It does not authorize cleanup, retries, ordering assertions or production execution.
func (m ExistingFeedManifest) VerifyNative(index int, rows []ExistingFeedNativeObservation) error {
	evidence, _, err := m.Prepare(index)
	if err != nil {
		return err
	}
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Labels, evidence.Expected.Labels) || !rows[0].StartsAt.Equal(evidence.Expected.StartsAt) || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(rows[0].Fingerprint) {
		return fmt.Errorf("existing-feed Native identity missing, ambiguous or different from frozen fixture")
	}
	return nil
}
