package evidence

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

const installedBundleVersion = "corrtest.oscar/installed-bundle-v1"
const installedBundleEntries = 1375
const installedBundleBytes = 152 << 20

var installedEntryPattern = regexp.MustCompile(`^(original-[0-9]{3}\.bin|observation-[0-9]{4}\.bin|outcome-[0-9]{2}\.json)$`)
var installedObservationPath = regexp.MustCompile(`^(preflight/(before|after)/(packs|load|mapping|rules|rule|runtime)\.json|preflight/absence/(active-raw-rows|history-page-[0-9]{3})\.json|observations/[0-9]{3}/(active-raw-rows|audit-raw-rows|history-page-[0-9]{3})\.json)$`)
var installedOriginalPath = regexp.MustCompile(`^(attempt\.json|completed\.json|[0-9]{4}-(before|after)\.json|kit/(manifest\.json|manifest\.sha256|README\.md|build_operational\.py|correlation-rules\.zip|corrtest_operational-0\.1\.0\.tar\.gz(\.sha256)?|flood-installed\.json|hash-vectors\.json|install_operational\.py))$`)

type installedArchiveAttempt struct {
	Evidence   oscar.InstalledPackInputEvidence `json:"evidence"`
	RecordedAt time.Time                        `json:"recordedAt"`
	Outcome    string                           `json:"outcome"`
}
type installedArchiveState struct {
	Run          domain.Run                         `json:"run"`
	Registration *storage.InstalledPackRegistration `json:"registration"`
	Attempts     []installedArchiveAttempt          `json:"attempts"`
}
type installedArchiveOutcome struct {
	BodySHA256     string                         `json:"bodySha256"`
	Result         oscar.InstalledPackInputResult `json:"result"`
	TransportError string                         `json:"transportError"`
	CompletedAt    time.Time                      `json:"completedAt"`
}
type installedArchiveIndex struct {
	Entry    string          `json:"entry"`
	Artifact domain.Artifact `json:"artifact"`
}
type installedArchivePlan struct {
	RunID            string                       `json:"runId"`
	Pattern          string                       `json:"pattern"`
	InputPath        string                       `json:"inputPath"`
	Kit              testpack.Bindings            `json:"kit"`
	Installation     testpack.InstallationBinding `json:"installation"`
	APITargetID      string                       `json:"apiTargetId"`
	ReceiverTargetID string                       `json:"receiverTargetId"`
	ReceiverBaseURL  string                       `json:"receiverBaseUrl"`
}

// WriteInstalledPack captures original run artifacts and outcomes. It validates
// offline correspondence before atomic publication; it grants no target authority.
func WriteInstalledPack(ctx context.Context, destination string, s storage.InstalledPackExportSnapshot, attachments []Attachment) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(s.Artifacts) > 1356 || len(attachments) > 1356 || len(s.Events) > 2048 || (s.Snapshot != nil && len(s.Snapshot.Attempts) > 15) {
		return Result{}, fmt.Errorf("installed capture count exceeds bound")
	}
	files := map[string][]byte{}
	state := installedArchiveState{Run: s.Run}
	if s.Snapshot != nil {
		r := s.Snapshot.Registration
		state.Registration = &r
		for i, a := range s.Snapshot.Attempts {
			header := installedArchiveAttempt{Evidence: a.Evidence, RecordedAt: a.RecordedAt}
			if a.Result != nil {
				if a.CompletedAt == nil {
					return Result{}, fmt.Errorf("partial outcome")
				}
				header.Outcome = fmt.Sprintf("outcome-%02d.json", i)
				raw, err := json.Marshal(installedArchiveOutcome{a.Evidence.Request.BodySHA256, *a.Result, a.TransportError, *a.CompletedAt})
				if err != nil {
					return Result{}, err
				}
				files[header.Outcome] = raw
			} else if a.CompletedAt != nil || a.TransportError != "" {
				return Result{}, fmt.Errorf("unrecorded outcome")
			}
			state.Attempts = append(state.Attempts, header)
		}
	}
	supplied := map[string]Attachment{}
	attachmentBytes := map[string]int64{}
	attachmentCounts := map[string]int{}
	for _, a := range attachments {
		if _, ok := supplied[a.Name]; ok {
			return Result{}, fmt.Errorf("duplicate attachment")
		}
		class := "observation"
		limit := 16 << 20
		totalLimit := int64(64 << 20)
		countLimit := 1024
		if strings.HasPrefix(a.Name, "runs/"+s.Run.ID+"/installed/original/") {
			class = "original"
			limit = 1 << 20
			totalLimit = 42 << 20
			countLimit = 332
		}
		attachmentBytes[class] += int64(len(a.Content))
		attachmentCounts[class]++
		if len(a.Content) > limit || attachmentBytes[class] > totalLimit || attachmentCounts[class] > countLimit {
			return Result{}, fmt.Errorf("installed attachment class bound")
		}
		if a.ByteSize != int64(len(a.Content)) || a.SHA256 != installedHashBytes(a.Content) {
			return Result{}, fmt.Errorf("attachment differs from captured hash")
		}
		supplied[a.Name] = a
	}
	ledger := append([]domain.Artifact(nil), s.Artifacts...)
	sort.Slice(ledger, func(i, j int) bool { return ledger[i].RelativePath < ledger[j].RelativePath })
	index := []installedArchiveIndex{}
	counts := map[string]int{}
	for _, a := range ledger {
		class, err := installedArtifactClass(s.Run.ID, a)
		if err != nil {
			return Result{}, err
		}
		item := installedArchiveIndex{Artifact: a}
		if a.Availability == domain.ArtifactAvailable {
			found, ok := supplied[a.RelativePath]
			if !ok || found.SHA256 != a.SHA256 || found.ByteSize != a.ByteSize {
				return Result{}, fmt.Errorf("available artifact missing or differs")
			}
			format := "observation-%04d.bin"
			if class == "original" {
				format = "original-%03d.bin"
			}
			item.Entry = fmt.Sprintf(format, counts[class])
			counts[class]++
			files[item.Entry] = bytes.Clone(found.Content)
			delete(supplied, a.RelativePath)
		}
		index = append(index, item)
	}
	if len(supplied) != 0 {
		return Result{}, fmt.Errorf("attachment is not in captured ledger")
	}
	for name, v := range map[string]any{"installed-run.json": state, "installed-index.json": index, "events.json": s.Events} {
		raw, err := json.Marshal(v)
		if err != nil {
			return Result{}, err
		}
		files[name] = raw
	}
	m := manifest{APIVersion: installedBundleVersion, RunID: s.Run.ID, CreatedAt: s.Run.UpdatedAt.UTC()}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m.Files = append(m.Files, manifestEntry{n, installedHashBytes(files[n]), int64(len(files[n]))})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Result{}, err
	}
	files["manifest.json"] = raw
	if err = validateInstalledFiles(ctx, files); err != nil {
		return Result{}, err
	}
	return publishInstalledFiles(ctx, destination, files, m.CreatedAt)
}

func installedHashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func installedDecode(raw []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing metadata JSON")
	}
	normalized, err := json.Marshal(v)
	if err != nil || !bytes.Equal(normalized, raw) {
		return fmt.Errorf("metadata must retain exact canonical typed bytes")
	}
	return nil
}
func installedArtifactClass(run string, a domain.Artifact) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	prefix := "runs/" + run + "/installed/"
	if run == "" || strings.ContainsAny(run, "/\\") || a.RunID != run || !strings.HasPrefix(a.RelativePath, prefix) || a.ID != run+":"+strings.TrimPrefix(a.RelativePath, "runs/"+run+"/") || a.Kind != "installed-pack" || a.CaseID != "" || a.MIMEType != "application/octet-stream" || a.RedactionState != "private-original-response" {
		return "", fmt.Errorf("foreign installed artifact")
	}
	name := strings.TrimPrefix(a.RelativePath, prefix)
	if strings.HasPrefix(name, "original/") && installedOriginalPath.MatchString(strings.TrimPrefix(name, "original/")) {
		return "original", nil
	}
	if installedObservationPath.MatchString(name) {
		return "observation", nil
	}
	return "", fmt.Errorf("unexpected installed artifact path")
}

func validateInstalledFiles(ctx context.Context, files map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(files) > installedBundleEntries {
		return fmt.Errorf("installed archive entry limit")
	}
	totals := map[string]int64{}
	counts := map[string]int{}
	for name, raw := range files {
		class, limit, err := installedZipClass(name)
		if err != nil {
			return err
		}
		if len(raw) > limit {
			return fmt.Errorf("installed entry exceeds class bound")
		}
		counts[class]++
		totals[class] += int64(len(raw))
	}
	if counts["original"] > 332 || counts["observation"] > 1024 || counts["outcome"] > 15 || counts["metadata"] != 4 || totals["original"] > 42<<20 || totals["observation"] > 64<<20 || totals["outcome"] > 30<<20 || totals["metadata"] > 16<<20 {
		return fmt.Errorf("installed class budget exceeded")
	}
	var m manifest
	if err := installedDecode(files["manifest.json"], &m); err != nil {
		return err
	}
	if m.APIVersion != installedBundleVersion || m.RunID == "" || m.CreatedAt.IsZero() || len(m.Files) != len(files)-1 {
		return fmt.Errorf("installed manifest differs")
	}
	seen := map[string]bool{"manifest.json": true}
	for _, f := range m.Files {
		raw, ok := files[f.Path]
		if !ok || seen[f.Path] || f.ByteSize != int64(len(raw)) || f.SHA256 != installedHashBytes(raw) {
			return fmt.Errorf("installed file inventory differs")
		}
		seen[f.Path] = true
	}
	var state installedArchiveState
	var index []installedArchiveIndex
	var events []domain.RunEvent
	for _, v := range []struct {
		name string
		dst  any
	}{{"installed-run.json", &state}, {"installed-index.json", &index}, {"events.json", &events}} {
		if err := installedDecode(files[v.name], v.dst); err != nil {
			return err
		}
	}
	run := state.Run
	if err := run.Validate(); err != nil {
		return err
	}
	if run.ID != m.RunID || !run.UpdatedAt.Equal(m.CreatedAt) || (run.Status != domain.RunCompleted && run.Status != domain.RunInterrupted) || len(events) > 2048 || len(index) > 1356 {
		return fmt.Errorf("installed terminal identity/bounds differ")
	}
	var plan installedArchivePlan
	if err := installedPlanDecode(run.CompiledPlanJSON, &plan); err != nil {
		return err
	}
	if plan.RunID != run.ID || plan.InputPath != "installed-test-pack" || plan.Pattern != "flood" {
		return fmt.Errorf("installed plan differs")
	}
	var planFields map[string]json.RawMessage
	if err := json.Unmarshal(run.CompiledPlanJSON, &planFields); err != nil {
		return err
	}
	if len(planFields) == 8 {
		if plan.APITargetID != run.TargetID || plan.ReceiverTargetID == "" || plan.ReceiverBaseURL == "" {
			return fmt.Errorf("full installed target binding differs")
		}
	} else if state.Registration != nil {
		return fmt.Errorf("registered run requires full installed plan")
	}
	var attemptEvents []domain.RunEvent
	var sequence int64
	for _, e := range events {
		if e.RunID != run.ID || e.Sequence <= sequence || e.OccurredAt.IsZero() || e.Type == "" || e.Level == "" {
			return fmt.Errorf("installed event identity differs")
		}
		sequence = e.Sequence
		if e.Type == "alert.attempting" {
			attemptEvents = append(attemptEvents, e)
		}
	}
	used := map[string]bool{"manifest.json": true, "installed-run.json": true, "installed-index.json": true, "events.json": true}
	paths := map[string]bool{}
	ids := map[string]bool{}
	originals := map[string][]byte{}
	observations := map[string][]byte{}
	classCounts := map[string]int{}
	for _, item := range index {
		a := item.Artifact
		class, err := installedArtifactClass(run.ID, a)
		if err != nil {
			return err
		}
		classCounts[class]++
		if classCounts["original"] > 332 || classCounts["observation"] > 1024 || paths[a.RelativePath] || ids[a.ID] {
			return fmt.Errorf("duplicate/oversized installed ledger")
		}
		paths[a.RelativePath] = true
		ids[a.ID] = true
		if a.Availability == domain.ArtifactPending {
			if item.Entry != "" {
				return fmt.Errorf("pending artifact has bytes")
			}
			continue
		}
		raw, ok := files[item.Entry]
		if !ok || used[item.Entry] || !strings.HasPrefix(item.Entry, class+"-") || a.ByteSize != int64(len(raw)) || a.SHA256 != installedHashBytes(raw) {
			return fmt.Errorf("installed ledger file differs")
		}
		used[item.Entry] = true
		if class == "original" {
			originals[strings.TrimPrefix(a.RelativePath, "runs/"+run.ID+"/installed/original/")] = raw
		} else {
			observations[a.RelativePath] = raw
		}
	}
	var snapshot *storage.InstalledPackSnapshot
	if state.Registration == nil {
		if len(state.Attempts) != 0 || len(attemptEvents) != 0 {
			return fmt.Errorf("attempt without original registration")
		}
	} else {
		r := *state.Registration
		snapshot = &storage.InstalledPackSnapshot{Registration: r}
		if r.RunID != run.ID || r.APITargetID != run.TargetID || r.Kit != plan.Kit || r.Installation != plan.Installation || r.Spec.ReceiverTargetID != plan.ReceiverTargetID || r.Spec.ReceiverBaseURL != plan.ReceiverBaseURL {
			return fmt.Errorf("registered run plan differs")
		}
		for i, a := range state.Attempts {
			attempt := storage.InstalledPackAttempt{Evidence: a.Evidence, RecordedAt: a.RecordedAt}
			if a.Outcome != "" {
				if a.Outcome != fmt.Sprintf("outcome-%02d.json", i) || used[a.Outcome] {
					return fmt.Errorf("outcome reference differs")
				}
				var out installedArchiveOutcome
				if err := installedDecode(files[a.Outcome], &out); err != nil {
					return err
				}
				if out.BodySHA256 != a.Evidence.Request.BodySHA256 {
					return fmt.Errorf("outcome request differs")
				}
				attempt.Result = &out.Result
				attempt.TransportError = out.TransportError
				attempt.CompletedAt = &out.CompletedAt
				used[a.Outcome] = true
			}
			snapshot.Attempts = append(snapshot.Attempts, attempt)
		}
		if _, err := storage.ValidateInstalledPackSnapshot(*snapshot, attemptEvents); err != nil {
			return err
		}
	}
	if len(used) != len(files) {
		return fmt.Errorf("unreferenced installed bytes")
	}
	if snapshot != nil {
		want := domain.CleanupNotRequired
		if len(snapshot.Attempts) > 0 {
			want = domain.CleanupDirty
			for _, a := range snapshot.Attempts {
				if a.Result == nil || a.Result.Class != oscar.InjectionForwarded || a.TransportError != "" {
					want = domain.CleanupUnknown
				}
			}
		}
		if run.CleanupStatus != want && !(run.CleanupStatus == domain.CleanupUnknown && (run.Status == domain.RunInterrupted || (run.Verdict == domain.VerdictInconclusive && run.TerminalError != ""))) {
			return fmt.Errorf("input responsibility changed")
		}
		if err := validateInstalledOriginalInventory(*state.Registration, originals); err != nil {
			return err
		}
	}
	var kit *testpack.Kit
	if snapshot != nil || (plan.Installation.JournalOperations > 0 && len(originals) == plan.Installation.JournalOperations*2+12) {
		var err error
		kit, err = loadInstalledOriginals(ctx, plan, originals)
		if err != nil {
			return err
		}
		if snapshot != nil && (kit.Scenario() != snapshot.Registration.Scenario || !reflect.DeepEqual(kit.Files(), snapshot.Registration.KitFiles)) {
			return fmt.Errorf("original kit inventory differs")
		}
	}
	return validateInstalledClaims(ctx, run, plan, snapshot, events, index, observations, kit)
}

func validateInstalledOriginalInventory(r storage.InstalledPackRegistration, originals map[string][]byte) error {
	if len(originals) != len(r.InstallationFiles) {
		return fmt.Errorf("original inventory incomplete")
	}
	for _, f := range r.InstallationFiles {
		raw, ok := originals[f.Path]
		if !ok || int64(len(raw)) != f.Size || installedHashBytes(raw) != f.SHA256 {
			return fmt.Errorf("original installation differs")
		}
	}
	return nil
}
func loadInstalledOriginals(ctx context.Context, plan installedArchivePlan, originals map[string][]byte) (loaded *testpack.Kit, failure error) {
	root, err := os.MkdirTemp("", "corrtest-installed-verify-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
			loaded = nil
			failure = fmt.Errorf("private original staging cleanup: %w", cleanupErr)
		}
	}()
	if err = os.Mkdir(filepath.Join(root, "kit"), 0700); err != nil {
		return nil, err
	}
	for name, raw := range originals {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !installedOriginalPath.MatchString(name) || path.Clean(name) != name {
			return nil, fmt.Errorf("unsafe original staging path")
		}
		if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), raw, 0600); err != nil {
			return nil, err
		}
	}
	kit, err := testpack.Load(filepath.Join(root, "kit"), plan.Kit.ManifestSHA256)
	if err != nil {
		return nil, err
	}
	var attempt struct {
		Ownership json.RawMessage `json:"ownership_declaration"`
	}
	if err = json.Unmarshal(originals["attempt.json"], &attempt); err != nil {
		return nil, err
	}
	installation, err := testpack.LoadInstallation(root, kit, plan.Installation.TargetOrigin, attempt.Ownership)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if kit.Bindings() != plan.Kit || installation.Binding() != plan.Installation {
		return nil, fmt.Errorf("reloaded original binding differs")
	}
	return kit, nil
}

func installedZipClass(name string) (string, int, error) {
	switch name {
	case "manifest.json", "installed-run.json", "installed-index.json", "events.json":
		return "metadata", 16 << 20, nil
	}
	if !installedEntryPattern.MatchString(name) {
		return "", 0, fmt.Errorf("unexpected installed ZIP name")
	}
	if strings.HasPrefix(name, "original-") {
		return "original", 1 << 20, nil
	}
	if strings.HasPrefix(name, "outcome-") {
		return "outcome", 2 << 20, nil
	}
	return "observation", 16 << 20, nil
}
func verifyInstalledPack(ctx context.Context, reader *zip.ReadCloser) error {
	if len(reader.File) > installedBundleEntries {
		return fmt.Errorf("installed entry count exceeds bound")
	}
	counts := map[string]int{}
	sizes := map[string]uint64{}
	seen := map[string]bool{}
	for _, f := range reader.File {
		class, limit, err := installedZipClass(f.Name)
		if err != nil {
			return err
		}
		if !f.Mode().IsRegular() || f.UncompressedSize64 > uint64(limit) || seen[f.Name] {
			return fmt.Errorf("unsafe/duplicate installed entry")
		}
		seen[f.Name] = true
		counts[class]++
		sizes[class] += f.UncompressedSize64
	}
	if counts["metadata"] != 4 || counts["original"] > 332 || counts["observation"] > 1024 || counts["outcome"] > 15 || sizes["metadata"] > 16<<20 || sizes["original"] > 42<<20 || sizes["observation"] > 64<<20 || sizes["outcome"] > 30<<20 {
		return fmt.Errorf("installed declared class budget exceeded")
	}
	files := map[string][]byte{}
	for _, f := range reader.File {
		raw, err := readZipEntry(ctx, f)
		if err != nil {
			return err
		}
		files[f.Name] = raw
	}
	return validateInstalledFiles(ctx, files)
}

func publishInstalledFiles(ctx context.Context, destination string, files map[string][]byte, at time.Time) (Result, error) {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return Result{}, err
	}
	if _, err = os.Lstat(destination); err == nil {
		return Result{}, fmt.Errorf("destination exists")
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return Result{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".corrtest-installed-*")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return Result{}, err
	}
	hash := sha256.New()
	counter := &countWriter{writer: io.MultiWriter(file, hash)}
	z := zip.NewWriter(counter)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err = ctx.Err(); err != nil {
			z.Close()
			return Result{}, err
		}
		header := &zip.FileHeader{Name: n, Method: zip.Deflate}
		header.SetMode(0600)
		header.SetModTime(at)
		w, e := z.CreateHeader(header)
		if e != nil {
			return Result{}, e
		}
		if _, e = w.Write(files[n]); e != nil {
			return Result{}, e
		}
	}
	if err = z.Close(); err != nil {
		return Result{}, err
	}
	if err = file.Sync(); err != nil {
		return Result{}, err
	}
	if err = file.Close(); err != nil {
		return Result{}, err
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	if err = os.Link(file.Name(), destination); err != nil {
		return Result{}, err
	}
	return Result{destination, hex.EncodeToString(hash.Sum(nil)), counter.count}, nil
}

// Recognize only existing retained mode locations, never arbitrary body strings.
func installedModeClaim(raw []byte) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	for key, part := range value {
		var s string
		if json.Unmarshal(part, &s) == nil && ((strings.EqualFold(key, "inputPath") && s == "installed-test-pack") || (strings.EqualFold(key, "apiVersion") && s == "corrtest.oscar/installed-run-v1")) {
			return true
		}
		if strings.EqualFold(key, "coverage") {
			var fields map[string]json.RawMessage
			if json.Unmarshal(part, &fields) == nil {
				for field, text := range fields {
					if strings.EqualFold(field, "inputPath") && json.Unmarshal(text, &s) == nil && s == "installed-test-pack" {
						return true
					}
				}
			}
		}
	}
	return false
}
