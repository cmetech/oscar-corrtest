package runner

import (
	"context"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Audit absence is not evidence of healthy no-match. The only accepted witness
// is consume.evaluate's explicit no_matching_rules decision for a fresh source.
func (r *Runner) readNoMatchAudits(ctx context.Context, snapshot observationSnapshot) (observationSnapshot, error) {
	byID := map[int]oscar.AuditRecord{}
	ready := snapshot.SourceReady && snapshot.SourceEpisodesReady
	for _, source := range snapshot.Sources {
		if source.Status != "firing" {
			ready = false
		}
		key := auditFingerprint(source)
		rows, err := r.api.CorrelationAudit(ctx, key)
		if err != nil {
			return observationSnapshot{}, err
		}
		witnessed := false
		for _, row := range rows {
			if row.AlertFingerprint != key {
				continue
			}
			if prior, found := byID[row.ID]; found && prior.AlertFingerprint != row.AlertFingerprint {
				ready = false
			}
			byID[row.ID] = row
			snapshot.Audits = append(snapshot.Audits, row)
			if row.ID <= 0 || row.CreatedAt.IsZero() || row.CreatedAt.Before(source.StartsAt) || row.CreatedAt.After(r.opts.Now()) {
				continue
			}
			if row.RuleIdentityNull && row.RuleID == 0 && row.RuleName == "" && row.Pattern == "" && row.Outcome == "released_no_trigger" && row.Reason == "no_matching_rules" && row.ParentFingerprint == "" && len(row.SuppressedNotifiers) == 0 {
				witnessed = true
			} else {
				ready = false
			}
		}
		if witnessed {
			snapshot.NoMatchCount++
		} else {
			ready = false
		}
	}
	snapshot.NoMatchReady = ready
	snapshot.AuditReady = ready
	return snapshot, nil
}
