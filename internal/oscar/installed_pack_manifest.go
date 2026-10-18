package oscar

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

const installedInputVersion = "corrtest.oscar/installed-input-v1"

var installedQualificationToken = regexp.MustCompile(`^[0-9a-f]{32}$`)

// InstalledPackInputSpec contains reproducible request coordinates, not proof of
// a verified kit, an installation, exclusive ownership, or permission to send.
type InstalledPackInputSpec struct {
	APIVersion       string    `json:"apiVersion"`
	ManifestSHA256   string    `json:"manifestSha256"`
	QualificationRun string    `json:"qualificationRun"`
	StartsAt         time.Time `json:"startsAt"`
	ReceiverTargetID string    `json:"receiverTargetId"`
	ReceiverBaseURL  string    `json:"receiverBaseUrl"`
}

// InstalledPackManifest is a pure fixed-format preparer with detached outputs.
// A standalone manifest does not authorize sending; the network sender retains
// its separate actual Kit, target, credential and transport checks.
type InstalledPackManifest struct {
	spec              InstalledPackInputSpec
	profile, revision string
	scenario          testpack.Scenario
}

func FreezeInstalledPackInput(spec InstalledPackInputSpec) (InstalledPackManifest, error) {
	_, offset := spec.StartsAt.Zone()
	if spec.APIVersion != installedInputVersion || !installedDigest.MatchString(spec.ManifestSHA256) || !installedQualificationToken.MatchString(spec.QualificationRun) || spec.StartsAt.Unix() <= 0 || spec.StartsAt.Year() > 9998 || spec.StartsAt.Nanosecond() != 0 || offset != 0 {
		return InstalledPackManifest{}, fmt.Errorf("installed input requires fixed version, digest, qualification token and whole-second UTC start")
	}
	if spec.ReceiverTargetID == "" || len(spec.ReceiverTargetID) > 256 || !utf8.ValidString(spec.ReceiverTargetID) || strings.TrimSpace(spec.ReceiverTargetID) != spec.ReceiverTargetID || strings.ContainsFunc(spec.ReceiverTargetID, unicode.IsControl) {
		return InstalledPackManifest{}, fmt.Errorf("installed input requires bounded receiver identity")
	}
	if _, err := installedBaseURL(spec.ReceiverBaseURL, "/webhookreceiver"); err != nil {
		return InstalledPackManifest{}, err
	}
	profile, revision, scenario := testpack.FixedInstalledInputContract()
	spec.StartsAt = spec.StartsAt.UTC()
	return InstalledPackManifest{spec: spec, profile: profile, revision: revision, scenario: scenario}, nil
}

func (m InstalledPackManifest) Spec() InstalledPackInputSpec { return m.spec }

func installedInputContractMatches(binding testpack.Bindings, scenario testpack.Scenario) bool {
	profile, revision, fixed := testpack.FixedInstalledInputContract()
	return installedDigest.MatchString(binding.ManifestSHA256) && binding.ProfileName == profile && binding.ProfileEffectiveSHA256 == revision && scenario == fixed
}

// Prepare is the sole fixed input body implementation. It has no I/O or clock.
func (m InstalledPackManifest) Prepare(index int) (InstalledPackInputEvidence, []byte, error) {
	if m.spec.APIVersion != installedInputVersion || index < 0 || index >= m.scenario.InputCount() {
		return InstalledPackInputEvidence{}, nil, fmt.Errorf("installed input requires frozen manifest and index 0..14")
	}
	site, position := "", index
	for _, group := range m.scenario.Groups {
		if position < group.Count {
			site = group.Site
			break
		}
		position -= group.Count
	}
	labels := map[string]string{"alertname": "CorrTestOperationalFlood", "qualification_pack": "corrtest_operational_v1", "qualification_run": m.spec.QualificationRun, "site": site, "source_id": fmt.Sprintf("%s-%02d", m.spec.QualificationRun, index), "severity": "warning"}
	body, err := json.Marshal(struct {
		Labels   map[string]string `json:"labels"`
		StartsAt time.Time         `json:"startsAt"`
		EndsAt   time.Time         `json:"endsAt"`
	}{labels, m.spec.StartsAt, m.spec.StartsAt.Add(time.Duration(m.scenario.Budgets.ValiditySeconds) * time.Second)})
	if err != nil {
		return InstalledPackInputEvidence{}, nil, err
	}
	sum := sha256.Sum256(body)
	path := "/api/v1/webhookreceiver/webhook/" + m.profile
	route, _ := installedBaseURL(m.spec.ReceiverBaseURL, "/webhookreceiver")
	if route.prefix != "" {
		path = "/webhook/" + m.profile
	}
	request := InjectionRequestEvidence{Format: "corrtest-installed-pack-request-v1", TargetID: m.spec.ReceiverTargetID, APIProfile: m.profile, Method: http.MethodPost, Endpoint: m.spec.ReceiverBaseURL + path, ContentType: "application/json", BodyBytes: len(body), BodySHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body), ProfilePrecondition: `"` + m.revision + `"`}
	return InstalledPackInputEvidence{ManifestSHA256: m.spec.ManifestSHA256, RunID: m.spec.QualificationRun, Index: index, Request: request, Expected: ExistingFeedIdentity{Labels: labels, StartsAt: m.spec.StartsAt}}, body, nil
}
