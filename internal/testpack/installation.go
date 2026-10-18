package testpack

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const installationJSONLimit = 32 << 20
const installationHTTPMax = 262144
const installationRules = "/api/v1/correlation_rules"
const installationPacks = "/api/v1/packs/corrtest_operational"

var installationUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var installationOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)
var installationLimits = []any{
	"ownership is operator-declared, not machine-verified exclusion",
	"readback cannot detect change/use/restore between requests",
	"runtime API supplies no immutable process identity",
	"installation verification is not scenario PASS or source cleanup proof",
	"offline expected receiver revision requires If-Match and acknowledgement validation during input",
}

// InstallationBinding contains original retained facts, not target authority.
// The validated runtime has only RuleID loaded and both dispatch flags false.
type InstallationBinding struct {
	AttemptID, TargetOrigin, ManifestSHA256         string
	ProjectOperationID, LoadID                      string
	RuleID                                          int
	LoadedAt                                        string
	LastAttemptMS                                   int64
	JournalSHA256, CompletedSHA256, OwnershipSHA256 string
	JournalOperations                               int
}

// Installation is a private copied snapshot. Trusted operator transfer and
// continuous lab exclusion remain external prerequisites, not observed facts.
type Installation struct {
	binding InstallationBinding
	files   []ArtifactBinding
	blobs   map[string][]byte
}

func (i *Installation) Binding() InstallationBinding {
	if i == nil {
		return InstallationBinding{}
	}
	return i.binding
}
func (i *Installation) Files() []ArtifactBinding {
	if i == nil {
		return nil
	}
	return append([]ArtifactBinding(nil), i.files...)
}
func (i *Installation) Evidence(name string) ([]byte, bool) {
	if i == nil {
		return nil, false
	}
	raw, ok := i.blobs[name]
	return bytes.Clone(raw), ok
}

// LoadInstallation validates only the fixed Python installer's original records.
// It makes no HTTP calls, executes no saved command and never changes evidence.
// expectedOwnershipJSON is a separately supplied operator declaration, not proof.
func LoadInstallation(directory string, kit *Kit, expectedOrigin string, expectedOwnershipJSON []byte) (*Installation, error) {
	if kit == nil || len(kit.blobs) != 10 || len(kit.manifest) == 0 || !digestPattern.MatchString(kit.bindings.ManifestSHA256) {
		return nil, fmt.Errorf("verified kit required")
	}
	if !installationOrigin(expectedOrigin) {
		return nil, fmt.Errorf("exact target origin required")
	}
	owner, err := parseJSON(expectedOwnershipJSON)
	if err != nil {
		return nil, fmt.Errorf("ownership declaration: %w", err)
	}
	ownership, err := object(owner, "format", "target", "owner_reference", "exclusive_through_export", "stable_configuration_and_processes", "destinations_history_or_controlled_lab")
	if err != nil || !installationHas(ownership, map[string]any{"format": "corrtest-owned-target-v1", "target": expectedOrigin, "exclusive_through_export": true, "stable_configuration_and_processes": true, "destinations_history_or_controlled_lab": true}) || !installationOwner.MatchString(installationString(ownership, "owner_reference")) {
		return nil, fmt.Errorf("explicit original operator declaration required")
	}
	blobs, err := snapshotInstallation(directory)
	if err != nil {
		return nil, err
	}
	for name, raw := range kit.blobs {
		if !bytes.Equal(blobs["kit/"+name], raw) {
			return nil, fmt.Errorf("original copied kit differs")
		}
	}
	attempt, err := installationJSON(blobs["attempt.json"])
	if err != nil {
		return nil, err
	}
	if _, err = object(attempt, "format", "attempt_id", "target", "manifest_sha256", "ownership_declaration", "ownership_observed", "limits", "initial_responsibility"); err != nil {
		return nil, err
	}
	common := map[string]any{"target": expectedOrigin, "manifest_sha256": kit.bindings.ManifestSHA256, "ownership_declaration": ownership, "ownership_observed": false, "limits": installationLimits}
	if !installationHas(attempt, common) || !installationHas(attempt, map[string]any{"format": "corrtest-install-attempt-v1", "initial_responsibility": "UNKNOWN-until-completed"}) || !installationUUID.MatchString(installationString(attempt, "attempt_id")) {
		return nil, fmt.Errorf("original attempt differs")
	}
	completed, err := installationJSON(blobs["completed.json"])
	if err != nil {
		return nil, err
	}
	if _, err = object(completed, "format", "status", "target", "manifest_sha256", "pack_content_hash", "project_operation_id", "load_id", "rule_id", "profile_effective_sha256", "profile_revision_observed", "authored_rule_sha256", "runtime_binding", "ownership_declaration", "ownership_observed", "limits", "scope", "journal_operations", "journal_sha256"); err != nil {
		return nil, err
	}
	if !installationHas(completed, common) || !installationHas(completed, map[string]any{"format": "corrtest-install-completed-v1", "status": "completed", "pack_content_hash": kit.bindings.PackContentHash, "profile_effective_sha256": kit.bindings.ProfileEffectiveSHA256, "profile_revision_observed": false, "authored_rule_sha256": kit.bindings.RuleAuthoredSHA256, "scope": "installation-readback-only-not-scenario-PASS"}) {
		return nil, fmt.Errorf("original completion differs")
	}
	ruleID, ok := completed["rule_id"].(int64)
	if !ok || ruleID <= 0 || ruleID > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("original rule ID required")
	}
	count, ok := completed["journal_operations"].(int64)
	if !ok || count <= 0 || count > 160 {
		return nil, fmt.Errorf("original journal operation bound")
	}
	b := InstallationBinding{AttemptID: installationString(attempt, "attempt_id"), TargetOrigin: expectedOrigin, ManifestSHA256: kit.bindings.ManifestSHA256, ProjectOperationID: installationString(completed, "project_operation_id"), LoadID: installationString(completed, "load_id"), RuleID: int(ruleID), JournalOperations: int(count), JournalSHA256: installationString(completed, "journal_sha256"), CompletedSHA256: digest(blobs["completed.json"]), OwnershipSHA256: digest(encodeCanonical(ownership))}
	if !installationUUID.MatchString(b.ProjectOperationID) || !installationUUID.MatchString(b.LoadID) || !digestPattern.MatchString(b.JournalSHA256) {
		return nil, fmt.Errorf("original receipt identity required")
	}
	rb, err := object(completed["runtime_binding"], "loaded_at", "last_attempt_ms", "loaded_rule_ids", "authoritative", "dispatch_enabled")
	if err != nil {
		return nil, err
	}
	b.LoadedAt = installationString(rb, "loaded_at")
	b.LastAttemptMS, _ = rb["last_attempt_ms"].(int64)
	if _, err := time.Parse(time.RFC3339Nano, b.LoadedAt); err != nil || b.LastAttemptMS <= 0 || !installationHas(rb, map[string]any{"loaded_rule_ids": []any{ruleID}, "authoritative": false, "dispatch_enabled": false}) {
		return nil, fmt.Errorf("original audit runtime binding required")
	}
	rows := make([]installationPair, 0, count)
	image := []any{}
	for n := int64(1); n <= count; n++ {
		pair := installationPair{}
		for j, suffix := range []string{"before", "after"} {
			name := fmt.Sprintf("%04d-%s.json", n, suffix)
			raw := blobs[name]
			v, e := installationJSON(raw)
			if e != nil {
				return nil, e
			}
			if j == 0 {
				pair.before = v
			} else {
				pair.after = v
			}
			image = append(image, map[string]any{"path": name, "bytes": int64(len(raw)), "sha256": digest(raw)})
		}
		if err := pair.validate(); err != nil {
			return nil, fmt.Errorf("original operation %d: %w", n, err)
		}
		rows = append(rows, pair)
	}
	if digest(encodeCanonical(image)) != b.JournalSHA256 {
		return nil, fmt.Errorf("original journal digest differs")
	}
	if err := validateInstallationRows(rows, kit, b, rb); err != nil {
		return nil, err
	}
	result := &Installation{binding: b, blobs: blobs}
	keys := make([]string, 0, len(blobs))
	for name := range blobs {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		raw := blobs[name]
		result.files = append(result.files, ArtifactBinding{name, int64(len(raw)), digest(raw)})
	}
	return result, nil
}
func installationOrigin(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || (u.Path != "" && u.Path != "/ext/mw") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\\# \t\r\n") || strings.Contains(u.Host, "%") || u.String() != raw {
		return false
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
func installationJSON(raw []byte) (map[string]any, error) {
	v, e := parseJSON(raw)
	if e != nil {
		return nil, e
	}
	m, ok := v.(map[string]any)
	if !ok || !bytes.Equal(encodeCanonical(v), raw) {
		return nil, fmt.Errorf("canonical original JSON object required")
	}
	return m, nil
}
func installationString(m map[string]any, key string) string { v, _ := m[key].(string); return v }
func installationHas(actual, want map[string]any) bool {
	for key, v := range want {
		got, ok := actual[key]
		if !ok || !bytes.Equal(encodeCanonical(got), encodeCanonical(v)) {
			return false
		}
	}
	return true
}
func installationExact(actual, want map[string]any) bool {
	return len(actual) == len(want) && installationHas(actual, want)
}

type installationPair struct{ before, after map[string]any }

func (p installationPair) request() map[string]any {
	v, _ := p.before["request"].(map[string]any)
	return v
}
func (p installationPair) body() map[string]any { v, _ := p.after["body"].(map[string]any); return v }
func (p installationPair) validate() error {
	if _, e := object(p.before, "operation", "responsibility", "request"); e != nil {
		return e
	}
	if p.before["responsibility"] != "UNKNOWN-until-recorded-response" || p.request() == nil {
		return fmt.Errorf("original request required")
	}
	if p.before["operation"] == "project-install" {
		_, e := object(p.after, "receipt")
		return e
	}
	if p.before["operation"] != "http" {
		return fmt.Errorf("unexpected operation")
	}
	if _, e := object(p.after, "status", "body_sha256", "body_bytes", "body"); e != nil {
		return e
	}
	status, ok := p.after["status"].(int64)
	size, sizeOK := p.after["body_bytes"].(int64)
	// body_sha256 describes unretained wire bytes, not canonical parsed body bytes.
	if !ok || status < 200 || status >= 300 || !sizeOK || size <= 0 || size > installationHTTPMax || !digestPattern.MatchString(installationString(p.after, "body_sha256")) || p.body() == nil {
		return fmt.Errorf("original successful bounded response required")
	}
	return nil
}

func validateInstallationRows(rows []installationPair, k *Kit, b InstallationBinding, runtime map[string]any) error {
	rule, _ := parseJSON(k.rule)
	state, _ := parseJSON(k.mappingState)
	artifact := map[string]any{"artifact_type": "mapping", "identity": map[string]any{"namespace": "webhook_profiles", "name": profileName}}
	list := installationRules + "?page=1&perPage=100&column=id&order=asc"
	mapping := installationPacks + "/configuration/items/" + k.bindings.MappingRowKey
	load := installationPacks + "/configuration/loads/" + b.LoadID
	status := load + "?page=1&per_page=100"
	pos := 0
	take := func(method, path string, extra map[string]any, validate func(map[string]any) bool) bool {
		if pos >= len(rows) {
			return false
		}
		p := rows[pos]
		pos++
		request := map[string]any{"method": method, "path": path}
		for key, value := range extra {
			request[key] = value
		}
		return p.before["operation"] == "http" && installationExact(p.request(), request) && (method != "GET" || p.after["status"] == int64(200)) && validate(p.body())
	}
	get := func(path string, validate func(map[string]any) bool) bool { return take("GET", path, nil, validate) }
	pack := func(installed bool) func(map[string]any) bool {
		return func(m map[string]any) bool {
			v, ok := m["packs"].([]any)
			if !ok || len(v) > 256 {
				return false
			}
			matches := 0
			for _, x := range v {
				r, ok := x.(map[string]any)
				if !ok {
					return false
				}
				if r["pack_id"] == packID {
					matches++
					if installed && !installationHas(r, map[string]any{"pack_version": "0.1.0", "manifest_status": "valid", "pack_content_hash": k.bindings.PackContentHash}) {
						return false
					}
				}
			}
			if installed {
				return matches == 1
			}
			return matches == 0
		}
	}
	rules := func(installed bool) func(map[string]any) bool {
		return func(m map[string]any) bool {
			v, ok := m["rows"].([]any)
			n := 0
			if installed {
				n = 1
			}
			if !ok || len(v) != n || !installationHas(m, map[string]any{"page": int64(1), "perPage": int64(100), "total": int64(n)}) {
				return false
			}
			if !installed {
				return true
			}
			r, ok := v[0].(map[string]any)
			return ok && r["id"] == int64(b.RuleID) && installationHas(r, rule.(map[string]any))
		}
	}
	runtimeMatches := func(installed bool) func(map[string]any) bool {
		return func(m map[string]any) bool {
			ids := []any{}
			var selected any
			total := int64(0)
			if installed {
				ids = []any{int64(b.RuleID)}
				total = 1
				selected = map[string]any{"id": int64(b.RuleID), "loaded": true, "rejection": nil}
			}
			if !installationHas(m, map[string]any{"authoritative": false, "dispatch_enabled": false, "load_status": "loaded", "loaded_rule_ids": ids, "loaded_total": total, "loaded_omitted": int64(0), "rejected_total": int64(0), "rejected_omitted": int64(0), "rejected_rules": []any{}, "selected_rule": selected}) {
				return false
			}
			dependencies, ok := m["dependencies"].(map[string]any)
			if !ok || !installationHas(dependencies, map[string]any{"status": "ready", "nats": true, "consumer": true, "redis": true, "lua_registered": true, "timer_store": true}) {
				return false
			}
			last, ok := m["last_attempt_ms"].(int64)
			checked, ok2 := m["checked_at_ms"].(int64)
			if !ok || !ok2 || last <= 0 || checked <= 0 || installationString(m, "loaded_at") == "" {
				return false
			}
			return !installed || installationHas(m, runtime)
		}
	}
	compare := func(m map[string]any) bool {
		return installationHas(m, artifact) && installationHas(m, map[string]any{"row_key": k.bindings.MappingRowKey, "state": "up_to_date", "changed_fields": []any{}, "pack": state, "live": state, "pack_hash": k.bindings.MappingStateSHA256, "live_hash": k.bindings.MappingStateSHA256, "applied_hash": k.bindings.MappingStateSHA256})
	}
	fail := func() error { return fmt.Errorf("original installation stage %d differs or is incomplete", pos) }
	if !get("/api/v1/packs", pack(false)) || !get(list, rules(false)) || !get(installationRules+"/runtime", runtimeMatches(false)) || !take("POST", installationRules+"/validate", map[string]any{"body": rule}, func(m map[string]any) bool {
		return installationHas(m, map[string]any{"valid": true, "errors": []any{}})
	}) {
		return fail()
	}
	if pos >= len(rows) {
		return fail()
	}
	project := rows[pos]
	pos++
	command := installationString(project.request(), "command")
	if project.before["operation"] != "project-install" || !path.IsAbs(command) || path.Base(command) != "oscar" || !installationExact(project.request(), map[string]any{"command": command, "archive": projectArchive, "archive_sha256": k.bindings.ProjectArchiveSHA256}) {
		return fail()
	}
	receipt, ok := project.after["receipt"].(map[string]any)
	if !ok || !installationHas(receipt, map[string]any{"status": "complete", "command": "install", "pack_id": packID, "old_version": nil, "new_version": "0.1.0", "manifest_sha256": k.bindings.PackManifestSHA256, "operation_id": b.ProjectOperationID}) {
		return fail()
	}
	for _, key := range []string{"inserted", "updated", "disabled", "purged", "schedules_changed", "renamed", "preapplied", "missing", "quarantined"} {
		if receipt[key] != int64(0) {
			return fail()
		}
	}
	if !get("/api/v1/packs", pack(true)) || !take("POST", installationPacks+"/configuration/preview", nil, func(m map[string]any) bool {
		if !installationHas(m, map[string]any{"pack_id": packID, "pack_version": "0.1.0", "pack_content_hash": k.bindings.PackContentHash, "counts": map[string]any{"rule": int64(0), "mapping": int64(1), "role": int64(0), "dashboard": int64(0)}}) {
			return false
		}
		items, ok := m["items"].([]any)
		if !ok || len(items) != 1 {
			return false
		}
		item, ok := items[0].(map[string]any)
		return ok && installationHas(item, artifact) && installationHas(item, map[string]any{"row_key": k.bindings.MappingRowKey, "source_file": mappingPath, "source_ordinal": int64(0), "action": "create", "state": "new", "live_hash": nil, "expected_live_hash": nil, "pack_hash": k.bindings.MappingStateSHA256})
	}) {
		return fail()
	}
	if !take("POST", installationPacks+"/configuration/loads", map[string]any{"body": map[string]any{"pack_content_hash": k.bindings.PackContentHash, "overwrite": []any{}, "preconditions": []any{map[string]any{"artifact": artifact, "expected_live_hash": nil}}}}, func(m map[string]any) bool {
		return installationHas(m, map[string]any{"load_id": b.LoadID, "status": "queued"}) && (m["status_url"] == load || m["status_url"] == b.TargetOrigin+load)
	}) || rows[pos-1].after["status"] != int64(202) {
		return fail()
	}
	completed := false
	for polls := 0; polls < 120; polls++ {
		if !get(status, func(m map[string]any) bool {
			if !installationHas(m, map[string]any{"load_id": b.LoadID, "pack_id": packID}) {
				return false
			}
			if m["status"] == "queued" || m["status"] == "running" || m["activation_status"] == "pending" || m["activation_status"] == "running" {
				return true
			}
			if !installationHas(m, map[string]any{"status": "completed", "activation_status": "succeeded", "activation_errors": []any{}, "created_count": int64(1), "replaced_count": int64(0), "skipped_count": int64(0), "failed_count": int64(0), "page": int64(1), "per_page": int64(100), "total": int64(1), "activation_targets": []any{map[string]any{"artifact_type": "mapping", "namespace": "webhook_profiles"}}}) {
				return false
			}
			items, ok := m["items"].([]any)
			if !ok || len(items) != 1 {
				return false
			}
			item, ok := items[0].(map[string]any)
			completed = ok && installationHas(item, artifact) && installationHas(item, map[string]any{"source_file": mappingPath, "source_ordinal": int64(0), "intended_action": "create", "result": "created", "applied_hash": k.bindings.MappingStateSHA256, "error_code": nil})
			return completed
		}) {
			return fail()
		}
		if completed {
			break
		}
	}
	if !completed || !get(mapping, compare) || !get(list, rules(false)) || !get(installationRules+"/runtime", runtimeMatches(false)) {
		return fail()
	}
	if !take("POST", installationRules+"/import", map[string]any{"file_sha256": digest(k.blobs["correlation-rules.zip"]), "file_bytes": int64(len(k.blobs["correlation-rules.zip"]))}, func(m map[string]any) bool {
		return installationExact(m, map[string]any{"created": []any{ruleName}, "updated": []any{}, "errors": []any{}})
	}) {
		return fail()
	}
	if !get(list, rules(true)) || !get(installationRules+"/"+strconv.Itoa(b.RuleID), func(m map[string]any) bool {
		return m["id"] == int64(b.RuleID) && installationHas(m, rule.(map[string]any))
	}) || !get(installationRules+"/runtime?rule_id="+strconv.Itoa(b.RuleID), runtimeMatches(true)) || !get("/api/v1/packs", pack(true)) || !get(mapping, compare) || pos != len(rows) {
		return fail()
	}
	return nil
}
