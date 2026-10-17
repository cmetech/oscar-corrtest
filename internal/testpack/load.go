// Package testpack verifies only the fixed offline initial-Flood kit. Pinned
// bytes and validator provenance are not installation or target authority.
package testpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrUnsupportedPlatform means this platform has no qualified no-follow kit snapshot.
var ErrUnsupportedPlatform = errors.New("offline kit loading is unsupported on this platform")

const maxBytes = 1 << 20
const packID = "corrtest_operational"
const profileName = "corrtest_operational_v1"
const ruleName = "corrtest-operational-v1-flood"
const projectArchive = "corrtest_operational-0.1.0.tar.gz"
const mappingPath = "conf/mappings/corrtest_operational_v1.json"
const effectiveSHA = "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb"
const mappingSHA = "dede2e20b3e35bf702057a06b58d8ae774820cb6034877660432419a94714e05"
const ruleSHA = "59bb6cc088808a038fbf8df0754fa7bcf786f1f1c0cacfce0068d428f5ed1e66"
const scenarioSHA = "e70535d0435f07a9f13d8b9a12fd4953878ed7a6316afae1f2ca314e41b60d0a"

var names = [8]string{"README.md", "build_operational.py", "correlation-rules.zip", projectArchive, projectArchive + ".sha256", "flood-installed.json", "hash-vectors.json", "install_operational.py"}
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sourcePattern = regexp.MustCompile(`^(oscar-util/scripts/project_pack|oscar-correlator/src/app|oscar-webhookreceiver/src/app)/[A-Za-z0-9_./-]+\.py$`)

// All fields in these bindings are values. No reference points at retained data.
type Bindings struct{ PackID, Version, ManifestSHA256, ProjectArchiveSHA256, PackManifestSHA256, PackContentHash, ProfileName, ProfileNamespace, ProfileMappingSHA256, ProfileEffectiveSHA256, RuleName, RuleAuthoredSHA256, MappingStateSHA256, MappingRowKey, MappingSourceFile string }
type ArtifactBinding struct {
	Path   string
	Size   int64
	SHA256 string
}
type Group struct {
	Site                                           string
	Count, ExpectedEnriched, ExpectedParentEmitted int
}
type Budgets struct{ ValiditySeconds, ExecutionSeconds, WindowSeconds, ObservationLagSeconds, PollSeconds int }
type Scenario struct {
	ID, Coverage, Cleanup string
	Groups                [4]Group
	Budgets               Budgets
}

func (s Scenario) InputCount() int {
	n := 0
	for _, g := range s.Groups {
		n += g.Count
	}
	return n
}

type Kit struct {
	bindings                                        Bindings
	files                                           []ArtifactBinding
	blobs                                           map[string][]byte
	manifest, scenario, profile, rule, mappingState []byte
}

func (k *Kit) Bindings() Bindings                  { return k.bindings }
func (k *Kit) Files() []ArtifactBinding            { return append([]ArtifactBinding(nil), k.files...) }
func (k *Kit) Artifact(name string) ([]byte, bool) { v, ok := k.blobs[name]; return bytes.Clone(v), ok }
func (k *Kit) ManifestJSON() []byte                { return bytes.Clone(k.manifest) }
func (k *Kit) ScenarioJSON() []byte                { return bytes.Clone(k.scenario) }
func (k *Kit) ProfileJSON() []byte                 { return bytes.Clone(k.profile) }
func (k *Kit) RuleJSON() []byte                    { return bytes.Clone(k.rule) }
func (k *Kit) MappingStateJSON() []byte            { return bytes.Clone(k.mappingState) }
func (k *Kit) Scenario() Scenario {
	if len(k.scenario) == 0 {
		return Scenario{}
	}
	return Scenario{ID: "flood-installed-v1", Coverage: "ordinary-initial-flood-audit", Cleanup: "DIRTY-until-owned-target-teardown", Groups: [4]Group{{"positive", 5, 4, 1}, {"negative", 4, 4, 0}, {"isolated-a", 3, 3, 0}, {"isolated-b", 3, 3, 0}}, Budgets: Budgets{600, 180, 60, 30, 1}}
}
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

// Load snapshots exactly ten bounded files. Every returned semantic binding is
// derived from those same bytes. The pin must come from a separately trusted
// distribution; this function neither authenticates its provider nor runs Python.
func Load(directory, pinnedManifestSHA string) (*Kit, error) {
	if !digestPattern.MatchString(pinnedManifestSHA) {
		return nil, fmt.Errorf("pinned manifest SHA256 required")
	}
	blobs, e := snapshot(directory)
	if e != nil {
		return nil, e
	}
	raw := blobs["manifest.json"]
	if digest(raw) != pinnedManifestSHA || string(blobs["manifest.sha256"]) != pinnedManifestSHA+"\n" {
		return nil, fmt.Errorf("manifest pin/sidecar mismatch")
	}
	v, e := parseJSON(raw)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(encodeCanonical(v), raw) {
		return nil, fmt.Errorf("noncanonical manifest")
	}
	m, e := object(v, "apiVersion", "id", "version", "requires_oscar", "coverage", "scenario", "validation", "requires", "unsupported", "profile", "rule", "validator_sources", "files")
	if e != nil {
		return nil, e
	}
	metadata, e := parseJSON([]byte(metadataJSON))
	if e != nil {
		panic(e)
	}
	for key, want := range metadata.(map[string]any) {
		if !bytes.Equal(encodeCanonical(m[key]), encodeCanonical(want)) {
			return nil, fmt.Errorf("unsupported manifest %s", key)
		}
	}
	p, e := object(m["profile"], "name", "namespace", "nested_mappings", "effective_sha256", "mapping_sha256")
	if e != nil {
		return nil, e
	}
	nested, ok := p["nested_mappings"].([]any)
	if !ok || len(nested) != 0 || p["name"] != profileName || p["namespace"] != "webhook_profiles" || p["effective_sha256"] != effectiveSHA || p["mapping_sha256"] != mappingSHA {
		return nil, fmt.Errorf("unsupported fixed profile")
	}
	r, e := object(m["rule"], "name", "authored_sha256")
	if e != nil || r["name"] != ruleName || r["authored_sha256"] != ruleSHA {
		return nil, fmt.Errorf("unsupported fixed rule")
	}
	sources, ok := m["validator_sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > 1000 {
		return nil, fmt.Errorf("invalid validator provenance")
	}
	for path, h := range sources {
		s, ok := h.(string)
		if !ok || !digestPattern.MatchString(s) || !sourcePattern.MatchString(path) || strings.Contains(path, "//") {
			return nil, fmt.Errorf("invalid validator provenance")
		}
		for _, part := range strings.Split(path, "/") {
			if part == ".." || part == "." {
				return nil, fmt.Errorf("invalid validator path")
			}
		}
	}
	files, ok := m["files"].([]any)
	if !ok || len(files) != len(names) {
		return nil, fmt.Errorf("invalid artifact inventory")
	}
	k := &Kit{blobs: blobs, manifest: raw, bindings: Bindings{PackID: packID, Version: "0.1.0", ManifestSHA256: pinnedManifestSHA, ProjectArchiveSHA256: digest(blobs[projectArchive]), ProfileName: profileName, ProfileNamespace: "webhook_profiles", ProfileMappingSHA256: mappingSHA, ProfileEffectiveSHA256: effectiveSHA, RuleName: ruleName, RuleAuthoredSHA256: ruleSHA, MappingSourceFile: mappingPath}}
	for i, v := range files {
		f, e := object(v, "path", "size", "sha256")
		if e != nil {
			return nil, e
		}
		size, ok := f["size"].(int64)
		if !ok || size <= 0 || size > maxBytes || f["path"] != names[i] || size != int64(len(blobs[names[i]])) || f["sha256"] != digest(blobs[names[i]]) {
			return nil, fmt.Errorf("artifact binding mismatch")
		}
		k.files = append(k.files, ArtifactBinding{names[i], size, f["sha256"].(string)})
	}
	scenario, e := canonicalJSON(blobs["flood-installed.json"])
	if e != nil || digest(scenario) != scenarioSHA {
		return nil, fmt.Errorf("unsupported fixed fifteen-input scenario")
	}
	k.scenario = scenario
	if string(blobs[projectArchive+".sha256"]) != k.bindings.ProjectArchiveSHA256+"  "+projectArchive+"\n" {
		return nil, fmt.Errorf("project sidecar mismatch")
	}
	if e = k.readArchives(); e != nil {
		return nil, e
	}
	if e = k.vectors(); e != nil {
		return nil, e
	}
	return k, nil
}
func (k *Kit) vectors() error {
	v, e := parseJSON(k.blobs["hash-vectors.json"])
	if e != nil {
		return e
	}
	m, e := object(v, "format", "vectors")
	if e != nil || m["format"] != "corrtest-canonical-json-v1" {
		return fmt.Errorf("invalid vectors")
	}
	a, ok := m["vectors"].([]any)
	if !ok || len(a) != 4 {
		return fmt.Errorf("four original vectors required")
	}
	for i, v := range a {
		r, e := object(v, "value", "utf8_hex", "sha256")
		if e != nil {
			return e
		}
		raw := encodeCanonical(r["value"])
		if r["utf8_hex"] != hex.EncodeToString(raw) || r["sha256"] != digest(raw) {
			return fmt.Errorf("Python canonical vector mismatch")
		}
		if i == 2 && !bytes.Equal(raw, k.rule) || i == 3 && !bytes.Equal(raw, k.profile) {
			return fmt.Errorf("vector/archive identity mismatch")
		}
	}
	return nil
}
func (k *Kit) setMappingState(profile map[string]any) {
	state := map[string]any{}
	for key, v := range profile {
		state[key] = v
	}
	elements := append([]any(nil), profile["element"].([]any)...)
	sort.Slice(elements, func(i, j int) bool {
		a, b := elements[i].(map[string]any), elements[j].(map[string]any)
		if a["key"] == b["key"] {
			return a["value"].(string) < b["value"].(string)
		}
		return a["key"].(string) < b["key"].(string)
	})
	state["element"] = elements
	state["comment"] = nil
	state["additional_ref"] = nil
	state["metadata"] = []any{map[string]any{"key": "source_pack", "value": packID}}
	k.mappingState = encodeCanonical(state)
	k.bindings.MappingStateSHA256 = digest(k.mappingState[:len(k.mappingState)-1])
	identity := encodeCanonical(map[string]any{"namespace": "webhook_profiles", "name": profileName})
	k.bindings.MappingRowKey = "mapping:" + digest(identity[:len(identity)-1])
}

const metadataJSON = `{"apiVersion":"corrtest.oscar/test-pack-v1","id":"corrtest_operational","version":"0.1.0","requires_oscar":">=26.3.0,<27.0.0","coverage":"ordinary-initial-flood-audit","scenario":"flood-installed-v1","validation":"offline-only-reference-settings-26.3.0","requires":["exclusive-owned-target","no-competing-writers-or-rules-through-export","stable-configuration-and-processes-through-export","controlled-lab-destinations-history-only","authenticated-target-access","receiver-profile-reload-readback","correlation-rule-load-readback","active-history-audit-readiness","authoritative-false-and-dispatch-enabled-false","target-owned-teardown"],"unsupported":["dispatch","clear","refire","restart","faults","enhanced-admission","other-patterns"]}`
