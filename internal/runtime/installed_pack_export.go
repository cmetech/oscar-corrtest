package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
)

func (r *Runtime) exportInstalledPack(ctx context.Context, run domain.Run, destination string) (evidence.Result, bool, error) {
	var zero evidence.Result
	var plan struct {
		InputPath string `json:"inputPath"`
	}
	_ = json.Unmarshal(run.CompiledPlanJSON, &plan)
	registered, registrationErr := r.database.HasInstalledPackRegistration(ctx, run.ID)
	if registrationErr != nil {
		return zero, true, fmt.Errorf("cannot establish installed export responsibility: %w", registrationErr)
	}
	if !registered && run.InputPath() != "installed-test-pack" && plan.InputPath != "installed-test-pack" {
		return zero, false, nil
	}
	// Close must join export before releasing storage; a terminal row alone does
	// not prove the execution goroutine has finished publishing its artifacts.
	r.activeMu.Lock()
	key := "installed-export:" + run.ID
	_, running := r.active[run.ID]
	_, exporting := r.active[key]
	if r.closing || running || exporting {
		r.activeMu.Unlock()
		return zero, true, fmt.Errorf("installed export requires an inactive run and open runtime")
	}
	ctx, cancel := context.WithCancel(ctx)
	r.active[key] = cancel
	r.runWG.Add(1)
	r.activeMu.Unlock()
	defer func() { cancel(); r.activeMu.Lock(); delete(r.active, key); r.activeMu.Unlock(); r.runWG.Done() }()
	snapshot, err := r.database.ReadInstalledPackExport(ctx, run.ID)
	if err != nil {
		return zero, true, err
	}
	attachments := make([]evidence.Attachment, 0, len(snapshot.Artifacts))
	counts := [2]int{}
	sizes := [2]int64{}
	prefix := "runs/" + run.ID + "/installed/"
	for _, record := range snapshot.Artifacts {
		kind := 1
		limit := int64(16 << 20)
		if record.RunID != run.ID || record.Kind != "installed-pack" || !strings.HasPrefix(record.RelativePath, prefix) {
			return zero, true, fmt.Errorf("foreign installed artifact ledger entry")
		}
		if strings.HasPrefix(record.RelativePath, prefix+"original/") {
			kind = 0
			limit = 1 << 20
		}
		counts[kind]++
		sizes[kind] += record.ByteSize
		if counts[0] > 332 || counts[1] > 1024 || sizes[0] > 42<<20 || sizes[1] > 64<<20 || record.ByteSize > limit {
			return zero, true, fmt.Errorf("installed export artifact budget exceeded; evidence unchanged")
		}
		if record.Availability == domain.ArtifactPending {
			continue
		}
		m := artifact.Manifest{RelativePath: record.RelativePath, MIMEType: record.MIMEType, SHA256: record.SHA256, ByteSize: record.ByteSize}
		raw, err := r.artifacts.ReadVerifiedBounded(ctx, m, limit)
		if err != nil {
			return zero, true, err
		}
		attachments = append(attachments, evidence.Attachment{Name: record.RelativePath, Content: raw, SHA256: record.SHA256, ByteSize: record.ByteSize})
	}
	result, err := evidence.WriteInstalledPack(ctx, destination, snapshot, attachments)
	return result, true, err
}
