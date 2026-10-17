package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
)

const OwnedExistingFeedVersion = "corrtest.oscar/owned-existing-feed-v1"

// OwnedExistingFeedPlan applies only to an externally owned immutable fixture lab.
// OwnershipReference locates the trusted controller evidence; it does not prove it.
type OwnedExistingFeedPlan struct {
	APIVersion         string                         `json:"apiVersion"`
	Manifest           oscar.ExistingFeedManifestSpec `json:"manifest"`
	NativeTargetID     string                         `json:"nativeTargetId"`
	NativeOrigin       string                         `json:"nativeOrigin"`
	HistoryTargetID    string                         `json:"historyTargetId"`
	HistoryOrigin      string                         `json:"historyOrigin"`
	OwnershipReference string                         `json:"ownershipReference"`
}

func (plan OwnedExistingFeedPlan) Validate() error {
	if plan.APIVersion != OwnedExistingFeedVersion || strings.TrimSpace(plan.OwnershipReference) == "" || len(plan.OwnershipReference) > 1024 || strings.ContainsAny(plan.OwnershipReference, "\r\n") {
		return fmt.Errorf("owned existing-feed plan requires its fixed version and external ownership evidence reference")
	}
	if _, err := oscar.FreezeExistingFeedManifest(plan.Manifest); err != nil {
		return err
	}
	if strings.TrimSpace(plan.NativeTargetID) == "" || strings.TrimSpace(plan.HistoryTargetID) == "" || plan.NativeTargetID == plan.HistoryTargetID || plan.NativeTargetID == plan.Manifest.TargetID || plan.HistoryTargetID == plan.Manifest.TargetID {
		return fmt.Errorf("owned existing-feed requires three distinct explicit target identities")
	}
	for _, origin := range []string{plan.NativeOrigin, plan.HistoryOrigin} {
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(origin, "#") {
			return fmt.Errorf("owned existing-feed observer origin is invalid")
		}
	}
	return nil
}

type OwnedExistingFeedRunner struct {
	plan     OwnedExistingFeedPlan
	manifest oscar.ExistingFeedManifest
	database *storage.Database
	input    *oscar.ExistingFeedLabInput
	native   *oscar.ExistingFeedNativeObserver
	history  *oscar.ExistingFeedHistoryReader
	targets  []domain.Target
	poll     time.Duration
}

type existingFeedObservation struct {
	FixtureIndex int                                 `json:"fixtureIndex"`
	Native       oscar.ExistingFeedNativeObservation `json:"native"`
	History      oscar.HistoryRecord                 `json:"history"`
}

// NewOwnedExistingFeedRunner builds real, origin-bound adapters without network I/O.
func NewOwnedExistingFeedRunner(plan OwnedExistingFeedPlan, receiver, native, history domain.Target, database *storage.Database, options oscar.Options) (*OwnedExistingFeedRunner, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if database == nil || native.ID != plan.NativeTargetID || native.BaseURL != plan.NativeOrigin || history.ID != plan.HistoryTargetID || history.BaseURL != plan.HistoryOrigin {
		return nil, fmt.Errorf("owned existing-feed target binding drift")
	}
	manifest, _ := oscar.FreezeExistingFeedManifest(plan.Manifest)
	input, err := oscar.NewExistingFeedLabInput(manifest, receiver, options)
	if err != nil {
		return nil, err
	}
	nativeReader, err := oscar.NewExistingFeedNativeObserver(manifest, native, options)
	if err != nil {
		return nil, err
	}
	historyReader, err := oscar.NewExistingFeedHistoryReader(history, options)
	if err != nil {
		return nil, err
	}
	poll := options.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	return &OwnedExistingFeedRunner{plan: plan, manifest: manifest, database: database, input: input, native: nativeReader, history: historyReader, targets: []domain.Target{receiver, native, history}, poll: poll}, nil
}

// Execute has no resume, resend or physical-clear path. PASS covers identity only.
func (r *OwnedExistingFeedRunner) Execute(ctx context.Context, run domain.Run) (executionErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if run.Status != domain.RunQueued || run.TargetID != r.plan.HistoryTargetID {
		return fmt.Errorf("owned existing-feed requires a fresh queued run bound to History")
	}
	// The durable transition is also the competing/stale execution guard.
	if err := r.database.TransitionRun(ctx, run.ID, domain.RunPreflight, time.Now().UTC(), "Owned existing-feed identity preflight"); err != nil {
		return err
	}
	cleanup := domain.CleanupNotRequired
	observations := []existingFeedObservation{}
	coverage := map[string]string{"inputPath": "owned-existing-feed", "identity": "Native and public History only", "correlation": "unproved", "deliveryReceipt": "unproved", "semanticTask": "unproved", "physicalClear": "unproved", "ownership": "external trusted immutable lab precondition"}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		verdict := domain.VerdictPass
		if executionErr != nil {
			verdict = domain.VerdictError
		}
		saved, err := r.database.GetRun(finishCtx, run.ID)
		if err != nil {
			executionErr = errors.Join(executionErr, err)
			return
		}
		if executionErr != nil {
			if err = r.database.TransitionRun(finishCtx, run.ID, domain.RunCancelling, time.Now().UTC(), "Owned identity run stopped; no input retry"); err != nil {
				executionErr = errors.Join(executionErr, err)
				return
			}
		} else if saved.Status != domain.RunAsserting {
			executionErr = fmt.Errorf("owned identity lifecycle incomplete")
			return
		}
		if err = r.database.TransitionRun(finishCtx, run.ID, domain.RunCleaningUp, time.Now().UTC(), "Retain existing-feed responsibility; no physical clear"); err != nil {
			executionErr = errors.Join(executionErr, err)
			return
		}
		report, _ := json.Marshal(struct {
			Coverage     map[string]string         `json:"coverage"`
			Observations []existingFeedObservation `json:"observations"`
		}{coverage, observations})
		detail := ""
		if executionErr != nil {
			detail = executionErr.Error()
		}
		executionErr = errors.Join(executionErr, r.database.CompleteRun(finishCtx, run.ID, verdict, cleanup, report, detail, time.Now().UTC()))
	}()
	document, _ := json.Marshal(struct {
		RunID     string                `json:"runId"`
		Pattern   string                `json:"pattern"`
		InputPath string                `json:"inputPath"`
		Plan      OwnedExistingFeedPlan `json:"ownedExistingFeed"`
		Targets   []domain.Target       `json:"targets"`
	}{run.ID, "owned-existing-feed-identity", "owned-existing-feed", r.plan, r.targets})
	capabilities, _ := json.Marshal(coverage)
	if err := r.database.SetRunExecutionDocuments(ctx, run.ID, capabilities, document, time.Now().UTC()); err != nil {
		return err
	}
	if err := r.database.RegisterExistingFeed(ctx, run.ID, r.plan.Manifest, time.Now().UTC()); err != nil {
		return err
	}
	// Only the exact scoped absent Native identities and absent own-cohort History
	// permit input. Existing/foreign/ambiguous scoped rows cannot be adopted.
	for index := 0; index < 6; index++ {
		if _, err := r.native.Observe(ctx, index); !errors.Is(err, oscar.ErrExistingFeedNativeNotObserved) {
			return fmt.Errorf("owned fixture %d is not independently absent in Native: %v", index, err)
		}
		rows, err := r.findHistory(ctx, index, "")
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return fmt.Errorf("owned fixture %d already has History", index)
		}
	}
	if err := r.database.TransitionRun(ctx, run.ID, domain.RunSettingUp, time.Now().UTC(), "Externally prepared fixture lab; no rule creation"); err != nil {
		return err
	}
	if err := r.database.TransitionRun(ctx, run.ID, domain.RunInjecting, time.Now().UTC(), "Record and send six fixed existing-feed identities"); err != nil {
		return err
	}
	for index := 0; index < 6; index++ {
		recorded := false
		result, sendErr := r.input.SendRecorded(ctx, index, func(e oscar.ExistingFeedRequestEvidence) error {
			if err := r.database.RecordExistingFeedAttempt(ctx, run.ID, e, time.Now().UTC()); err != nil {
				return err
			}
			recorded = true
			cleanup = domain.CleanupUnknown
			return nil
		})
		if !recorded {
			return sendErr
		}
		evidence, _, _ := r.manifest.Prepare(index)
		outcomeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		detail := ""
		if sendErr != nil {
			detail = sendErr.Error()
		}
		err := r.database.CompleteExistingFeedAttempt(outcomeCtx, run.ID, r.plan.Manifest.Profile, index, evidence.Request.BodySHA256, result, detail, time.Now().UTC())
		cancel()
		if err != nil {
			return errors.Join(sendErr, err)
		}
		if sendErr != nil || result.Class != oscar.InjectionForwarded {
			return fmt.Errorf("owned fixture %d did not establish forwarding; no retry: %w", index, errors.Join(sendErr, fmt.Errorf("classification %s", result.Class)))
		}
		cleanup = domain.CleanupDirty
		observation, err := r.observe(ctx, run.ID, index)
		if err != nil {
			if observation.Native.Fingerprint != "" {
				observations = append(observations, observation)
			}
			return err
		}
		raw, _ := json.Marshal(observation)
		if _, err = r.database.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "existing_feed.identity_observed", Level: "info", OccurredAt: time.Now().UTC(), Summary: "Exact original Native and History firing identity observed", DetailJSON: string(raw)}); err != nil {
			return err
		}
		observations = append(observations, observation)
	}
	if err := r.database.TransitionRun(ctx, run.ID, domain.RunObserving, time.Now().UTC(), "Six original identities retained; correlation remains unproved"); err != nil {
		return err
	}
	return r.database.TransitionRun(ctx, run.ID, domain.RunAsserting, time.Now().UTC(), "Identity-only assertions passed")
}

func (r *OwnedExistingFeedRunner) findHistory(ctx context.Context, index int, nativeFingerprint string) ([]oscar.HistoryRecord, error) {
	e, _, _ := r.manifest.Prepare(index)
	rows, err := r.history.FindHistory(ctx, oscar.HistoryQuery{AlertName: e.Expected.Labels["alertname"], Start: r.plan.Manifest.StartsAt.Add(-time.Minute), End: time.Now().UTC().Add(time.Minute)})
	if err != nil {
		return nil, err
	}
	key := "node"
	if r.plan.Manifest.Profile == "middleware_custom_alert" {
		key = "hostname"
	}
	scoped := []oscar.HistoryRecord{}
	for _, row := range rows {
		if row.Labels[key] == e.Expected.Labels[key] || (nativeFingerprint != "" && row.Labels["am_fingerprint"] == nativeFingerprint) {
			scoped = append(scoped, row)
		}
	}
	return scoped, nil
}
func (r *OwnedExistingFeedRunner) observe(ctx context.Context, runID string, index int) (existingFeedObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := existingFeedObservation{FixtureIndex: index}
	for {
		native, err := r.native.Observe(ctx, index)
		if err == nil {
			result.Native = native
			raw, _ := json.Marshal(result)
			if _, err = r.database.AppendRunEvent(ctx, domain.RunEvent{RunID: runID, Type: "existing_feed.native_observed", Level: "info", OccurredAt: time.Now().UTC(), Summary: "Original Native labels, start and fingerprint retained before History observation", DetailJSON: string(raw)}); err != nil {
				return result, err
			}
			break
		}
		if !errors.Is(err, oscar.ErrExistingFeedNativeNotObserved) {
			return result, err
		}
		if err = sleepContext(ctx, r.poll); err != nil {
			return result, err
		}
	}
	for {
		rows, err := r.findHistory(ctx, index, result.Native.Fingerprint)
		if err != nil {
			return result, err
		}
		if len(rows) > 0 {
			if err = r.manifest.VerifyHistory(index, result.Native, rows); err != nil {
				return result, err
			}
			result.History = rows[0]
			return result, nil
		}
		if err = sleepContext(ctx, r.poll); err != nil {
			return result, err
		}
	}
}
