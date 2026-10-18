package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

const installedRegistrationVersion = "corrtest.oscar/installed-registration-v1"
const installedRegistrationLimit = 128 << 10

var installedSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)
var installedUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// InstalledPackRegistration retains original verified-object correspondence.
// It is not evidence of live preflight or remote exclusion. Register once after
// successful preflight with the captured start, before entering INJECTING.
// API/receiver pairing is frozen here; reads never adopt mutable target rows.
type InstalledPackRegistration struct {
	APIVersion        string                       `json:"apiVersion"`
	RunID             string                       `json:"runId"`
	APITargetID       string                       `json:"apiTargetId"`
	APIBaseURL        string                       `json:"apiBaseUrl"`
	Spec              oscar.InstalledPackInputSpec `json:"spec"`
	Kit               testpack.Bindings            `json:"kit"`
	Scenario          testpack.Scenario            `json:"scenario"`
	Installation      testpack.InstallationBinding `json:"installation"`
	KitFiles          []testpack.ArtifactBinding   `json:"kitFiles"`
	InstallationFiles []testpack.ArtifactBinding   `json:"installationFiles"`
}

type InstalledPackAttempt struct {
	Evidence       oscar.InstalledPackInputEvidence `json:"evidence"`
	Result         *oscar.InstalledPackInputResult  `json:"result"` // nil remains UNKNOWN, never permission to replay
	TransportError string                           `json:"transportError"`
	RecordedAt     time.Time                        `json:"recordedAt"`
	CompletedAt    *time.Time                       `json:"completedAt"`
}
type InstalledPackSnapshot struct {
	Registration InstalledPackRegistration `json:"registration"`
	Attempts     []InstalledPackAttempt    `json:"attempts"`
}
type installedPackResult struct {
	BodySHA256 string                         `json:"bodySha256"`
	Result     oscar.InstalledPackInputResult `json:"result"`
	Error      string                         `json:"error"`
}

func (d *Database) RegisterInstalledPack(ctx context.Context, runID string, kit *testpack.Kit, installation *testpack.Installation, spec oscar.InstalledPackInputSpec, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() || kit == nil || installation == nil {
		return fmt.Errorf("verified kit, installation and registration time required")
	}
	r := InstalledPackRegistration{APIVersion: installedRegistrationVersion, RunID: runID, Spec: spec, Kit: kit.Bindings(), Scenario: kit.Scenario(), Installation: installation.Binding(), KitFiles: kit.Files(), InstallationFiles: installation.Files()}
	// Use the actual objects' privately retained bytes, not caller-authored expected
	// JSON. This does not authenticate transferred files or prove target ownership.
	for _, f := range r.InstallationFiles {
		raw, ok := installation.Evidence(f.Path)
		if !ok || int64(len(raw)) != f.Size || installedHash(raw) != f.SHA256 {
			return fmt.Errorf("installation snapshot differs")
		}
		if strings.HasPrefix(f.Path, "kit/") {
			original, ok := kit.Artifact(strings.TrimPrefix(f.Path, "kit/"))
			if !ok || !bytes.Equal(original, raw) {
				return fmt.Errorf("installation belongs to a different kit")
			}
		}
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, receiverBase string
	err = tx.QueryRowContext(ctx, `SELECT r.status,t.id,t.base_url FROM runs r JOIN targets t ON t.id=r.target_id WHERE r.id=?`, runID).Scan(&status, &r.APITargetID, &r.APIBaseURL)
	if err != nil {
		return fmt.Errorf("original API target: %w", err)
	}
	if status != string(domain.RunPreflight) && status != string(domain.RunSettingUp) {
		return fmt.Errorf("installed registration requires pre-injection preflight state")
	}
	if err = tx.QueryRowContext(ctx, `SELECT base_url FROM targets WHERE id=?`, spec.ReceiverTargetID).Scan(&receiverBase); err != nil || receiverBase != spec.ReceiverBaseURL {
		return fmt.Errorf("original receiver target differs")
	}
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM existing_feeds WHERE run_id=?`, runID).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return fmt.Errorf("run already has an existing-feed contract")
	}
	if _, err = validateInstalledRegistration(r); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > installedRegistrationLimit {
		return fmt.Errorf("installed registration exceeds bound")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO installed_pack_runs(run_id,qualification_token,manifest_sha256,api_target_id,api_base_url,registration_json,registration_sha256,created_at) VALUES(?,?,?,?,?,?,?,?)`, runID, spec.QualificationRun, spec.ManifestSHA256, r.APITargetID, r.APIBaseURL, string(raw), installedHash(raw), formatTime(at))
	if err != nil {
		return fmt.Errorf("register installed pack: %w", err)
	}
	return tx.Commit()
}

// RecordInstalledPackAttempt commits the entire deterministic request, UNKNOWN
// responsibility and event in one transaction before the sender can issue HTTP.
func (d *Database) RecordInstalledPackAttempt(ctx context.Context, runID string, evidence oscar.InstalledPackInputEvidence, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("attempt time required")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, m, err := readInstalledPack(ctx, tx, runID)
	if err != nil {
		return err
	}
	if evidence.Index != len(s.Attempts) {
		return fmt.Errorf("installed input is not the next unattempted index")
	}
	for _, a := range s.Attempts {
		if a.Result == nil || a.Result.Class != oscar.InjectionForwarded || a.TransportError != "" {
			return fmt.Errorf("prior installed input is not durably forwarded")
		}
	}
	expected, _, err := m.Prepare(evidence.Index)
	if err != nil {
		return err
	}
	want, _ := json.Marshal(expected)
	raw, err := json.Marshal(evidence)
	if err != nil || !bytes.Equal(raw, want) {
		return fmt.Errorf("installed request differs from frozen input")
	}
	update, err := tx.ExecContext(ctx, `UPDATE runs SET cleanup_status='UNKNOWN',updated_at=? WHERE id=? AND status='INJECTING'`, formatTime(at), runID)
	if err != nil {
		return err
	}
	if err = exactlyOne(update); err != nil {
		return fmt.Errorf("installed input requires INJECTING: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO installed_pack_attempts(run_id,input_index,evidence_json,recorded_at) VALUES(?,?,?,?)`, runID, evidence.Index, string(raw), formatTime(at))
	if err != nil {
		return err
	}
	_, err = appendEvent(ctx, tx, domain.RunEvent{RunID: runID, Type: "alert.attempting", Level: "info", OccurredAt: at, Summary: "Installed-pack input responsibility recorded before send", DetailJSON: string(raw)})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) CompleteInstalledPackAttempt(ctx context.Context, runID string, index int, bodySHA string, result oscar.InstalledPackInputResult, transportError string, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("outcome time required")
	}
	if err := validateInstalledResult(result, transportError); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, _, err := readInstalledPack(ctx, tx, runID)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(s.Attempts) || s.Attempts[index].Result != nil || s.Attempts[index].Evidence.Request.BodySHA256 != bodySHA {
		return fmt.Errorf("outcome requires original unresolved input hash")
	}
	raw, err := json.Marshal(installedPackResult{BodySHA256: bodySHA, Result: result, Error: transportError})
	if err != nil {
		return err
	}
	update, err := tx.ExecContext(ctx, `UPDATE installed_pack_attempts SET result_json=?,completed_at=? WHERE run_id=? AND input_index=? AND result_json IS NULL`, string(raw), formatTime(at), runID, index)
	if err != nil {
		return err
	}
	if err = exactlyOne(update); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadInstalledPackSnapshot uses one transaction and performs no filesystem,
// credential or network reads. Original artifacts must also be compared during
// export; this local consistency check is not a signature or new authority.
func (d *Database) ReadInstalledPackSnapshot(ctx context.Context, runID string) (InstalledPackSnapshot, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return InstalledPackSnapshot{}, err
	}
	defer tx.Rollback()
	s, _, err := readInstalledPack(ctx, tx, runID)
	if err != nil {
		return InstalledPackSnapshot{}, err
	}
	return s, nil
}

func readInstalledPack(ctx context.Context, tx *sql.Tx, runID string) (InstalledPackSnapshot, oscar.InstalledPackManifest, error) {
	var s InstalledPackSnapshot
	var zero oscar.InstalledPackManifest
	var raw, hash, token, manifest, target, base, created string
	err := tx.QueryRowContext(ctx, `SELECT registration_json,registration_sha256,qualification_token,manifest_sha256,api_target_id,api_base_url,created_at FROM installed_pack_runs WHERE run_id=?`, runID).Scan(&raw, &hash, &token, &manifest, &target, &base, &created)
	if err != nil {
		return s, zero, err
	}
	if installedHash([]byte(raw)) != hash || decodeInstalled(raw, installedRegistrationLimit, &s.Registration) != nil {
		return s, zero, fmt.Errorf("invalid installed registration bytes")
	}
	r := s.Registration
	if r.RunID != runID || r.Spec.QualificationRun != token || r.Spec.ManifestSHA256 != manifest || r.APITargetID != target || r.APIBaseURL != base {
		return s, zero, fmt.Errorf("installed registration columns differ")
	}
	if _, err = parseInstalledTime(created); err != nil {
		return s, zero, err
	}
	m, err := validateInstalledRegistration(r)
	if err != nil {
		return s, zero, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT input_index,evidence_json,result_json,recorded_at,completed_at FROM installed_pack_attempts WHERE run_id=? ORDER BY input_index LIMIT 16`, runID)
	if err != nil {
		return s, zero, err
	}
	s.Attempts = []InstalledPackAttempt{}
	for rows.Next() {
		var index int
		var evidenceRaw, recorded string
		var resultRaw, completed sql.NullString
		if err = rows.Scan(&index, &evidenceRaw, &resultRaw, &recorded, &completed); err != nil {
			rows.Close()
			return s, zero, err
		}
		if index != len(s.Attempts) || index >= 15 {
			rows.Close()
			return s, zero, fmt.Errorf("installed attempt sequence differs")
		}
		if index > 0 {
			prior := s.Attempts[index-1]
			if prior.Result == nil || prior.Result.Class != oscar.InjectionForwarded || prior.TransportError != "" {
				rows.Close()
				return s, zero, fmt.Errorf("installed attempt follows uncertain input")
			}
		}
		expected, _, e := m.Prepare(index)
		if e != nil {
			rows.Close()
			return s, zero, e
		}
		want, _ := json.Marshal(expected)
		if evidenceRaw != string(want) {
			rows.Close()
			return s, zero, fmt.Errorf("stored installed evidence differs")
		}
		at, e := parseInstalledTime(recorded)
		if e != nil {
			rows.Close()
			return s, zero, e
		}
		a := InstalledPackAttempt{Evidence: expected, RecordedAt: at}
		if resultRaw.Valid != completed.Valid {
			rows.Close()
			return s, zero, fmt.Errorf("partial installed outcome")
		}
		if resultRaw.Valid {
			var result installedPackResult
			if decodeInstalled(resultRaw.String, 2<<20, &result) != nil || result.BodySHA256 != expected.Request.BodySHA256 {
				rows.Close()
				return s, zero, fmt.Errorf("invalid stored installed outcome")
			}
			if e = validateInstalledResult(result.Result, result.Error); e != nil {
				rows.Close()
				return s, zero, e
			}
			end, e := parseInstalledTime(completed.String)
			if e != nil {
				rows.Close()
				return s, zero, e
			}
			a.Result = &result.Result
			a.TransportError = result.Error
			a.CompletedAt = &end
		}
		s.Attempts = append(s.Attempts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, zero, err
	}
	events, err := tx.QueryContext(ctx, `SELECT detail_json,occurred_at FROM run_events WHERE run_id=? AND type='alert.attempting' ORDER BY sequence LIMIT 16`, runID)
	if err != nil {
		return s, zero, err
	}
	defer events.Close()
	index := 0
	for events.Next() {
		var detail, occurred string
		if err = events.Scan(&detail, &occurred); err != nil {
			return s, zero, err
		}
		if index >= len(s.Attempts) {
			return s, zero, fmt.Errorf("unmatched installed attempt event")
		}
		want, _ := json.Marshal(s.Attempts[index].Evidence)
		if detail != string(want) || occurred != formatTime(s.Attempts[index].RecordedAt) {
			return s, zero, fmt.Errorf("installed event evidence differs")
		}
		index++
	}
	if err = events.Err(); err != nil {
		return s, zero, err
	}
	if index != len(s.Attempts) {
		return s, zero, fmt.Errorf("missing installed attempt event")
	}
	return s, m, nil
}

func validateInstalledRegistration(r InstalledPackRegistration) (oscar.InstalledPackManifest, error) {
	var zero oscar.InstalledPackManifest
	profile, revision, scenario := testpack.FixedInstalledInputContract()
	k := r.Kit
	i := r.Installation
	if r.APIVersion != installedRegistrationVersion || len(r.RunID) > 256 || !strings.HasPrefix(r.RunID, "crt_") || r.APITargetID == "" || len(r.APITargetID) > 256 || !utf8.ValidString(r.APITargetID) || r.Scenario != scenario || k.ProfileName != profile || k.ProfileEffectiveSHA256 != revision || k.ProfileNamespace != "webhook_profiles" || k.PackID != "corrtest_operational" || k.Version != "0.1.0" || k.RuleName != "corrtest-operational-v1-flood" || k.MappingSourceFile != "conf/mappings/corrtest_operational_v1.json" {
		return zero, fmt.Errorf("installed fixed contract differs")
	}
	if k.ManifestSHA256 != i.ManifestSHA256 || k.ManifestSHA256 != r.Spec.ManifestSHA256 || r.APIBaseURL != i.TargetOrigin || !installedAPIBase(r.APIBaseURL) {
		return zero, fmt.Errorf("original installation/kit/API binding differs")
	}
	for _, digest := range []string{k.ManifestSHA256, k.ProjectArchiveSHA256, k.PackManifestSHA256, k.ProfileMappingSHA256, k.ProfileEffectiveSHA256, k.RuleAuthoredSHA256, k.MappingStateSHA256, i.JournalSHA256, i.CompletedSHA256, i.OwnershipSHA256} {
		if !installedSHA.MatchString(digest) {
			return zero, fmt.Errorf("invalid original hash")
		}
	}
	if !strings.HasPrefix(k.PackContentHash, "sha256:") || !installedSHA.MatchString(strings.TrimPrefix(k.PackContentHash, "sha256:")) || !strings.HasPrefix(k.MappingRowKey, "mapping:") || !installedSHA.MatchString(strings.TrimPrefix(k.MappingRowKey, "mapping:")) || !installedUUID.MatchString(i.AttemptID) || !installedUUID.MatchString(i.ProjectOperationID) || !installedUUID.MatchString(i.LoadID) || i.RuleID <= 0 || i.LastAttemptMS <= 0 || i.JournalOperations < 1 || i.JournalOperations > 160 {
		return zero, fmt.Errorf("invalid original installation identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, i.LoadedAt); err != nil {
		return zero, fmt.Errorf("invalid original runtime timestamp")
	}
	if err := validateInstalledInventories(r); err != nil {
		return zero, err
	}
	return oscar.FreezeInstalledPackInput(r.Spec)
}

func validateInstalledInventories(r InstalledPackRegistration) error {
	expected := []string{"README.md", "build_operational.py", "correlation-rules.zip", "corrtest_operational-0.1.0.tar.gz", "corrtest_operational-0.1.0.tar.gz.sha256", "flood-installed.json", "hash-vectors.json", "install_operational.py"}
	if len(r.KitFiles) != 8 || len(r.InstallationFiles) != 2*r.Installation.JournalOperations+12 {
		return fmt.Errorf("incomplete original artifact inventory")
	}
	kit := map[string]testpack.ArtifactBinding{}
	for n, f := range r.KitFiles {
		if f.Path != expected[n] || f.Size <= 0 || f.Size > 1<<20 || !installedSHA.MatchString(f.SHA256) {
			return fmt.Errorf("invalid kit artifact inventory")
		}
		kit[f.Path] = f
	}
	if kit["corrtest_operational-0.1.0.tar.gz"].SHA256 != r.Kit.ProjectArchiveSHA256 {
		return fmt.Errorf("project archive binding differs")
	}
	files := map[string]testpack.ArtifactBinding{}
	previous := ""
	var total int64
	for _, f := range r.InstallationFiles {
		if f.Path <= previous || f.Size <= 0 || f.Size > 1<<20 || !installedSHA.MatchString(f.SHA256) {
			return fmt.Errorf("invalid installation artifact inventory")
		}
		previous = f.Path
		files[f.Path] = f
		total += f.Size
	}
	if total > 42<<20 {
		return fmt.Errorf("installation inventory exceeds snapshot bound")
	}
	for name, f := range kit {
		actual, ok := files["kit/"+name]
		if !ok || actual.Size != f.Size || actual.SHA256 != f.SHA256 {
			return fmt.Errorf("copied kit inventory differs")
		}
		delete(files, "kit/"+name)
	}
	if files["kit/manifest.json"].SHA256 != r.Kit.ManifestSHA256 || files["kit/manifest.sha256"].Path == "" || files["completed.json"].SHA256 != r.Installation.CompletedSHA256 || files["attempt.json"].Path == "" {
		return fmt.Errorf("original completion/manifest inventory differs")
	}
	for _, name := range []string{"kit/manifest.json", "kit/manifest.sha256", "completed.json", "attempt.json"} {
		delete(files, name)
	}
	var records []map[string]any
	var jsonBytes int64
	for index := 1; index <= r.Installation.JournalOperations; index++ {
		for _, phase := range []string{"before", "after"} {
			name := fmt.Sprintf("%04d-%s.json", index, phase)
			f, ok := files[name]
			if !ok {
				return fmt.Errorf("missing original mutation receipt")
			}
			jsonBytes += f.Size
			records = append(records, map[string]any{"path": name, "bytes": f.Size, "sha256": f.SHA256})
			delete(files, name)
		}
	}
	if len(files) != 0 || jsonBytes > 32<<20 {
		return fmt.Errorf("invalid original journal inventory")
	}
	raw, _ := json.Marshal(records)
	canonical, err := testpack.CanonicalJSON(raw)
	if err != nil || installedHash(canonical) != r.Installation.JournalSHA256 {
		return fmt.Errorf("original journal hash differs")
	}
	return nil
}
func validateInstalledResult(r oscar.InstalledPackInputResult, transportError string) error {
	if len(r.Body) > 1<<20 || len(transportError) > 4096 || !utf8.ValidString(transportError) || r.StatusCode < 0 || r.StatusCode > 599 || r.TaskID != "" || len(r.ProfileRevisions) > 32 {
		return fmt.Errorf("installed outcome exceeds fixed bounds")
	}
	for _, revision := range r.ProfileRevisions {
		if len(revision) > 256 || !utf8.ValidString(revision) {
			return fmt.Errorf("invalid profile acknowledgment")
		}
	}
	switch r.Class {
	case oscar.InjectionForwarded:
		_, revision, _ := testpack.FixedInstalledInputContract()
		body, err := testpack.CanonicalJSON(r.Body)
		if transportError != "" || r.StatusCode != 200 || err != nil || string(body) != "{\"status\":\"ok\"}\n" || len(r.ProfileRevisions) != 1 || r.ProfileRevisions[0] != revision {
			return fmt.Errorf("forwarded installed outcome lacks exact acknowledgment")
		}
	case oscar.InjectionIndeterminate:
	default:
		return fmt.Errorf("unsupported installed input outcome")
	}
	return nil
}
func decodeInstalled(raw string, limit int, dest any) error {
	if len(raw) == 0 || len(raw) > limit {
		return fmt.Errorf("installed JSON exceeds bound")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	canonical, err := json.Marshal(dest)
	if err != nil || string(canonical) != raw {
		return fmt.Errorf("installed JSON is not exact typed canonical bytes")
	}
	return nil
}
func installedHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func parseInstalledTime(raw string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() || formatTime(at) != raw {
		return time.Time{}, fmt.Errorf("invalid stored installed timestamp")
	}
	return at, nil
}
func installedAPIBase(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/ext/mw") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\\# \t\r\n") || strings.Contains(u.Host, "%") || u.String() != raw {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
