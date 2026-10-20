package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

const compactObservationFormat = "corrtest-compact-observation-v1"
const compactObservationPurpose = "Read-only observation; not scenario PASS, source coverage, delivery to external destinations, recovery or capacity proof."

type CompactObservationResult struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Outcome string `json:"outcome"`
}

// Bodies are base64 encoded by encoding/json to preserve original HTTP bytes.
type CompactConfigurationCapture struct {
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt time.Time         `json:"completed_at"`
	HTTPStatus  int               `json:"http_status"`
	Bodies      map[string][]byte `json:"bodies"`
}

type CompactEvidenceCapture struct {
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	HTTPStatus   int    `json:"http_status"`
	Body         []byte `json:"body"`
	FailureClass string `json:"failure_class"`
}

type CompactObservationReport struct {
	Format           string                       `json:"format"`
	Purpose          string                       `json:"purpose"`
	Outcome          string                       `json:"outcome"`
	FailureClass     string                       `json:"failure_class,omitempty"`
	Stage            string                       `json:"stage"`
	StartedAt        time.Time                    `json:"started_at"`
	CompletedAt      time.Time                    `json:"completed_at"`
	TargetID         string                       `json:"target_id"`
	TargetOrigin     string                       `json:"target_origin"`
	Kit              testpack.Bindings            `json:"kit"`
	Installation     testpack.InstallationBinding `json:"installation"`
	OriginalFiles    []testpack.ArtifactBinding   `json:"original_files"`
	Scope            *oscar.CompactEvidenceScope  `json:"scope,omitempty"`
	ConsumerManifest string                       `json:"consumer_manifest_sha256,omitempty"`
	Before           *CompactConfigurationCapture `json:"before,omitempty"`
	Evidence         *CompactEvidenceCapture      `json:"evidence,omitempty"`
	After            *CompactConfigurationCapture `json:"after,omitempty"`
}

func compactExpected(k *testpack.Kit, i *testpack.Installation) (oscar.InstalledPackExpected, oscar.InstalledPackBinding) {
	b, original := k.Bindings(), i.Binding()
	return oscar.InstalledPackExpected{ManifestSHA256: b.ManifestSHA256, PackID: b.PackID, Version: b.Version,
			PackContentHash: b.PackContentHash, MappingNamespace: b.ProfileNamespace, MappingName: b.ProfileName,
			MappingSourceFile: b.MappingSourceFile, MappingStateSHA256: b.MappingStateSHA256, MappingRowKey: b.MappingRowKey,
			RuleJSON: k.RuleJSON(), MappingStateJSON: k.MappingStateJSON()},
		oscar.InstalledPackBinding{TargetOrigin: original.TargetOrigin, ManifestSHA256: original.ManifestSHA256,
			LoadID: original.LoadID, RuleID: original.RuleID, Runtime: oscar.InstalledRuntimeBinding{
				LoadedAt: original.LoadedAt, LastAttemptMS: original.LastAttemptMS, LoadedRuleIDs: []int{original.RuleID}}}
}

func compactObservationScope(raw []byte, ruleID int) (oscar.CompactEvidenceScope, string, error) {
	var runtime struct {
		Authoritative   *bool `json:"authoritative"`
		DispatchEnabled *bool `json:"dispatch_enabled"`
		Enhanced        struct {
			ProductionEnabled *bool `json:"production_enabled"`
		} `json:"enhanced_correlation"`
		Compact struct {
			State string `json:"state"`
			Local struct {
				Owner    bool            `json:"owner_verified"`
				Handoff  bool            `json:"parent_handoff_ready"`
				Dispatch string          `json:"ordinary_correlator_dispatch"`
				Reason   json.RawMessage `json:"reason"`
				Scope    struct {
					RuleID   int64  `json:"rule_id"`
					Epoch    string `json:"epoch"`
					Revision string `json:"rule_revision"`
					Manifest string `json:"manifest_sha256"`
				} `json:"scope"`
			} `json:"local_readiness"`
		} `json:"compact_summary"`
	}
	invalid := func() (oscar.CompactEvidenceScope, string, error) {
		return oscar.CompactEvidenceScope{}, "", fmt.Errorf("compact runtime owner/scope unavailable")
	}
	if json.Unmarshal(raw, &runtime) != nil {
		return invalid()
	}
	local := runtime.Compact.Local
	if runtime.Authoritative == nil || *runtime.Authoritative || runtime.DispatchEnabled == nil || *runtime.DispatchEnabled ||
		runtime.Enhanced.ProductionEnabled == nil || *runtime.Enhanced.ProductionEnabled ||
		runtime.Compact.State != "running" || !local.Owner || !local.Handoff || local.Dispatch != "disabled" ||
		!bytes.Equal(bytes.TrimSpace(local.Reason), []byte("null")) || ruleID <= 0 || local.Scope.RuleID != int64(ruleID) ||
		!regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(local.Scope.Epoch) ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(local.Scope.Revision) ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(local.Scope.Manifest) {
		return invalid()
	}
	return oscar.CompactEvidenceScope{RuleID: local.Scope.RuleID, Epoch: local.Scope.Epoch, Revision: local.Scope.Revision}, local.Scope.Manifest, nil
}

func captureCompactConfiguration(ctx context.Context, reader *oscar.InstalledPackReader, expected oscar.InstalledPackExpected, binding oscar.InstalledPackBinding, current bool) (*CompactConfigurationCapture, error) {
	capture := &CompactConfigurationCapture{StartedAt: time.Now().UTC()}
	var value *oscar.InstalledPackObservation
	var err error
	if current {
		value, err = reader.ReadCurrentRuntime(ctx, expected, binding)
	} else {
		value, err = reader.Read(ctx, expected, binding)
	}
	capture.CompletedAt = time.Now().UTC()
	if err != nil {
		return capture, err
	}
	capture.HTTPStatus = 200
	capture.Bodies = map[string][]byte{"packs": value.Packs, "load": value.Load, "mapping": value.Mapping,
		"rules": value.Rules, "rule": value.Rule, "runtime": value.Runtime}
	if err := compactConfigurationBodiesBound(capture.Bodies); err != nil {
		capture.Bodies = nil
		return capture, err
	}
	return capture, nil
}

func compactConfigurationBodiesBound(bodies map[string][]byte) error {
	if len(bodies) != 6 {
		return fmt.Errorf("incomplete configuration capture")
	}
	total := 0
	for _, key := range []string{"packs", "load", "mapping", "rules", "rule", "runtime"} {
		total += len(bodies[key])
		if len(bodies[key]) == 0 || len(bodies[key]) > 1<<20 || total > 4<<20 {
			return fmt.Errorf("compact configuration retention bound exceeded")
		}
	}
	return nil
}

func compactConfigurationValue(c *CompactConfigurationCapture) (oscar.InstalledPackObservation, error) {
	if c == nil || c.HTTPStatus != 200 || c.StartedAt.IsZero() || c.CompletedAt.Before(c.StartedAt) {
		return oscar.InstalledPackObservation{}, fmt.Errorf("incomplete configuration capture")
	}
	if err := compactConfigurationBodiesBound(c.Bodies); err != nil {
		return oscar.InstalledPackObservation{}, err
	}
	return oscar.InstalledPackObservation{Packs: c.Bodies["packs"], Load: c.Bodies["load"], Mapping: c.Bodies["mapping"],
		Rules: c.Bodies["rules"], Rule: c.Bodies["rule"], Runtime: c.Bodies["runtime"]}, nil
}

// ValidateCompactObservation replays successful observations against separately
// verified original kit/installation objects. It performs no I/O and grants no
// source-coverage or scenario acceptance.
func ValidateCompactObservation(raw []byte, kit *testpack.Kit, installation *testpack.Installation) error {
	if len(raw) == 0 || len(raw) > compactObservationMaxBytes || kit == nil || installation == nil {
		return fmt.Errorf("compact observation requires bounded bytes and verified originals")
	}
	var report CompactObservationReport
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("invalid compact observation report")
	}
	// This new immutable artifact has exactly the writer's serialization. Refuse
	// duplicate/casefold aliases and edited normalization before semantic replay.
	encoded, err := json.Marshal(report)
	if err != nil || !bytes.Equal(encoded, raw) {
		return fmt.Errorf("compact observation original serialization differs")
	}
	if report.Format != compactObservationFormat || report.Purpose != compactObservationPurpose || report.Outcome != "observed" ||
		report.Stage != "complete" || report.FailureClass != "" || report.TargetID == "" ||
		report.StartedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) ||
		report.Kit != kit.Bindings() || report.Installation != installation.Binding() ||
		!reflect.DeepEqual(report.OriginalFiles, installation.Files()) || report.TargetOrigin != installation.Binding().TargetOrigin ||
		report.Scope == nil || report.Evidence == nil {
		return fmt.Errorf("compact observation binding or outcome differs")
	}
	expected, binding := compactExpected(kit, installation)
	before, err := compactConfigurationValue(report.Before)
	if err != nil {
		return err
	}
	binding, err = oscar.CurrentInstalledRuntimeBinding(expected, binding, before)
	if err != nil {
		return fmt.Errorf("compact current runtime binding unavailable")
	}
	for _, capture := range []*CompactConfigurationCapture{report.Before, report.After} {
		value, err := compactConfigurationValue(capture)
		if err != nil {
			return err
		}
		if err = oscar.ValidateInstalledPackObservation(expected, binding, value); err != nil {
			return fmt.Errorf("compact original configuration differs")
		}
		scope, manifest, err := compactObservationScope(value.Runtime, binding.RuleID)
		if err != nil || scope != *report.Scope || manifest != report.ConsumerManifest {
			return fmt.Errorf("compact scope changed")
		}
	}
	observation := oscar.CompactEvidenceObservation{StartedAt: report.Evidence.StartedAt, CompletedAt: report.Evidence.CompletedAt,
		HTTPStatus: report.Evidence.HTTPStatus, Body: report.Evidence.Body, FailureClass: report.Evidence.FailureClass}
	if err := oscar.ValidateCompactEvidenceObservation(*report.Scope, observation); err != nil {
		return err
	}
	start, err1 := time.Parse(time.RFC3339Nano, observation.StartedAt)
	end, err2 := time.Parse(time.RFC3339Nano, observation.CompletedAt)
	if err1 != nil || err2 != nil || report.Before.StartedAt.Before(report.StartedAt) ||
		start.Before(report.Before.CompletedAt) || report.After.StartedAt.Before(end) ||
		report.CompletedAt.Before(report.After.CompletedAt) {
		return fmt.Errorf("compact observation chronology differs")
	}
	return nil
}

func (r *Runtime) ObserveCompact(ctx context.Context, kit *testpack.Kit, installation *testpack.Installation, targetID, destination string) (result CompactObservationResult, resultErr error) {
	writer, err := prepareCompactObservationOutput(destination)
	if err != nil {
		return result, err
	}
	var release func()
	report := CompactObservationReport{Format: compactObservationFormat, Purpose: compactObservationPurpose,
		Outcome: "unavailable", Stage: "originals", StartedAt: time.Now().UTC(), TargetID: targetID}
	defer func() {
		defer func() {
			if release != nil {
				release()
			}
		}()
		defer writer.close()
		report.CompletedAt = time.Now().UTC()
		if resultErr != nil {
			report.Outcome = "unavailable"
			report.FailureClass = "observation_unavailable"
			if errors.Is(resultErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				report.FailureClass = "cancelled"
			}
		}
		raw, encodeErr := json.Marshal(report)
		if encodeErr == nil && report.Outcome == "observed" {
			encodeErr = ValidateCompactObservation(raw, kit, installation)
		}
		if encodeErr != nil {
			report.Outcome, report.FailureClass = "unavailable", "observation_validation"
			raw, _ = json.Marshal(report)
			resultErr = errors.Join(resultErr, fmt.Errorf("compact observation validation unavailable"))
		}
		if err := writer.publish(raw); err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		hash := sha256.Sum256(raw)
		result = CompactObservationResult{Path: writer.path, SHA256: hex.EncodeToString(hash[:]), Bytes: len(raw), Outcome: report.Outcome}
	}()
	if kit == nil || installation == nil || kit.Bindings().ManifestSHA256 != installation.Binding().ManifestSHA256 {
		return result, fmt.Errorf("compact observation original binding unavailable")
	}
	report.Kit, report.Installation, report.OriginalFiles = kit.Bindings(), installation.Binding(), installation.Files()
	report.TargetOrigin = installation.Binding().TargetOrigin
	for _, original := range installation.Files() {
		if len(original.Path) > 4 && original.Path[:4] == "kit/" {
			a, ok := installation.Evidence(original.Path)
			b, present := kit.Artifact(original.Path[4:])
			if !ok || !present || !bytes.Equal(a, b) {
				return result, fmt.Errorf("compact observation original kit differs")
			}
		}
	}
	r.activeMu.Lock()
	if r.closing {
		r.activeMu.Unlock()
		return result, fmt.Errorf("runtime is closing")
	}
	token, err := domain.NewRunID(rand.Reader)
	if err != nil {
		r.activeMu.Unlock()
		return result, fmt.Errorf("compact observation identity unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	stop := context.AfterFunc(r.rootCtx, cancel)
	key := "compact-observation:" + token.String()
	r.active[key] = cancel
	r.runWG.Add(1)
	r.activeMu.Unlock()
	release = func() {
		stop()
		cancel()
		r.activeMu.Lock()
		delete(r.active, key)
		r.activeMu.Unlock()
		r.runWG.Done()
	}
	report.Stage = "target"
	target, err := r.GetTarget(ctx, targetID)
	if err != nil || target.BaseURL != report.TargetOrigin {
		return result, fmt.Errorf("compact observation stored target differs")
	}
	options := oscar.Options{Getenv: r.getenv, HarnessVersion: r.version}
	reader, err := oscar.NewInstalledPackReader(target, options)
	if err != nil {
		return result, fmt.Errorf("compact observation configuration reader unavailable")
	}
	evidenceReader, err := oscar.NewCompactEvidenceReader(target, options)
	if err != nil {
		return result, fmt.Errorf("compact observation evidence reader unavailable")
	}
	expected, binding := compactExpected(kit, installation)
	report.Stage = "before"
	report.Before, err = captureCompactConfiguration(ctx, reader, expected, binding, true)
	if err != nil {
		return result, fmt.Errorf("compact observation preflight unavailable")
	}
	before, err := compactConfigurationValue(report.Before)
	if err != nil {
		return result, err
	}
	binding, err = oscar.CurrentInstalledRuntimeBinding(expected, binding, before)
	if err != nil {
		return result, fmt.Errorf("compact current runtime binding unavailable")
	}
	scope, manifest, err := compactObservationScope(report.Before.Bodies["runtime"], binding.RuleID)
	if err != nil {
		return result, err
	}
	report.Scope, report.ConsumerManifest = &scope, manifest
	report.Stage = "evidence"
	observation, err := evidenceReader.Read(ctx, scope)
	report.Evidence = &CompactEvidenceCapture{StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt,
		HTTPStatus: observation.HTTPStatus, Body: observation.Body, FailureClass: observation.FailureClass}
	if err != nil {
		return result, fmt.Errorf("compact evidence unavailable")
	}
	report.Stage = "after"
	report.After, err = captureCompactConfiguration(ctx, reader, expected, binding, false)
	if err != nil {
		return result, fmt.Errorf("compact observation postflight unavailable")
	}
	afterScope, afterManifest, err := compactObservationScope(report.After.Bodies["runtime"], binding.RuleID)
	if err != nil || scope != afterScope || manifest != afterManifest {
		return result, fmt.Errorf("compact observation scope changed")
	}
	report.Stage, report.Outcome = "complete", "observed"
	return result, nil
}
