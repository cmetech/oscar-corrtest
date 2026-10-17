package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
)

// existingFeedExport copies validated durable evidence; it never changes or
// infers outcomes and cannot discharge source responsibility.
func (r *Runtime) existingFeedExport(ctx context.Context, run domain.Run) (*evidence.Attachment, error) {
	var plan struct {
		RunID     string                       `json:"runId"`
		InputPath string                       `json:"inputPath"`
		Plan      runner.OwnedExistingFeedPlan `json:"ownedExistingFeed"`
	}
	decodeErr := json.Unmarshal(run.CompiledPlanJSON, &plan)
	if run.InputPath() != "owned-existing-feed" && plan.InputPath != "owned-existing-feed" {
		return nil, nil
	}
	if decodeErr != nil || plan.RunID != run.ID || plan.InputPath != "owned-existing-feed" || run.InputPath() != plan.InputPath || plan.Plan.HistoryTargetID != run.TargetID {
		return nil, fmt.Errorf("existing-feed export run/plan binding differs")
	}
	if err := plan.Plan.Validate(); err != nil {
		return nil, err
	}
	snapshot, err := r.database.ReadExistingFeedSnapshot(ctx, run.ID, plan.Plan.Manifest.Profile)
	if err != nil {
		return nil, fmt.Errorf("read existing-feed export snapshot: %w", err)
	}
	registered, _ := json.Marshal(snapshot.Manifest)
	planned, _ := json.Marshal(plan.Plan.Manifest)
	if !bytes.Equal(registered, planned) {
		return nil, fmt.Errorf("existing-feed export manifest differs from registration")
	}
	document := struct {
		APIVersion string `json:"apiVersion"`
		RunID      string `json:"runId"`
		storage.ExistingFeedSnapshot
	}{"corrtest.oscar/existing-feed-attempts-v1", run.ID, snapshot}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	digest := sha256.Sum256(raw)
	return &evidence.Attachment{Name: "existing-feed-attempts.json", Content: raw, SHA256: hex.EncodeToString(digest[:]), ByteSize: int64(len(raw))}, nil
}
