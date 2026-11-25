package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// maintPermission is what fo-01's role needs for the lane: every maintenance
// route accepts manage:, and patterns and templates need it.
const maintPermission = "manage:maintenance-windows"

// precheckMaint is B16's checks: it reads patterns, templates and the ledger,
// then creates and deletes a probe pattern (which proves manage:). The probe
// carries the campaign prefix, so a leftover one blocks the next B16.
func precheckMaint(ctx context.Context, ops MaintOps) (measured string, problems []string, err error) {
	denied := func(err error) bool {
		var se *targetops.StatusError
		return errors.As(err, &se) && (se.Status == http.StatusForbidden || se.Status == http.StatusUnauthorized)
	}
	pats, err := ops.MaintPatterns(ctx)
	if denied(err) {
		return "", []string{"fo-01 cannot read maintenance patterns (" + err.Error() + "): its role needs " + maintPermission}, nil
	}
	if err != nil {
		return "", nil, err
	}
	for _, p := range pats {
		if p.AlwaysApply {
			problems = append(problems, fmt.Sprintf("always-applied maintenance pattern %q would join the campaign window and mark matching alerts suppressed in History", p.Key))
		}
		if strings.HasPrefix(p.Key, maintPrefix) {
			problems = append(problems, "leftover campaign maintenance pattern "+p.Key)
		}
	}
	tmpls, err := ops.MaintTemplates(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, t := range tmpls {
		if strings.HasPrefix(t.Key, maintPrefix) {
			problems = append(problems, "leftover campaign maintenance template "+t.Key)
		}
	}
	recs, err := ops.MaintRecords(ctx, maintPrefix)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{}
	for _, rec := range recs {
		if !strings.HasPrefix(rec.CR, maintPrefix) || seen[rec.CR] {
			continue
		}
		seen[rec.CR] = true
		if err := maintWindowGone(ctx, ops, rec.CR); err != nil {
			problems = append(problems, "leftover campaign maintenance window: "+err.Error())
		}
	}
	measured = fmt.Sprintf("%d patterns, %d templates, %d campaign windows checked", len(pats), len(tmpls), len(seen))
	if len(problems) > 0 { // already blocked: write nothing (a first pattern write migrates the legacy MAINT_MODE_RULES list)
		return measured, problems, nil
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	probe := maintPrefix + "pre-" + hex.EncodeToString(b[:])
	err = ops.CreateMaintPattern(ctx, targetops.MaintPattern{Key: probe, Name: "CorrTest pre-check", Description: "CorrTest B16 probe; deleted at once",
		Match: []targetops.MaintMatch{{Label: maintLabel, Operator: "equals", Value: "precheck"}}, Combinator: "and"})
	switch {
	case denied(err):
		return measured, []string{"fo-01 cannot create maintenance patterns (" + err.Error() + "): its role needs " + maintPermission}, nil
	case err != nil:
		return measured, []string{"probe pattern " + probe + " not created: " + err.Error()}, nil
	}
	if err := ops.DeleteMaintPattern(ctx, probe); err != nil {
		return measured, []string{"probe pattern " + probe + " not deleted: " + err.Error()}, nil
	}
	all, err := ops.MaintPatterns(ctx)
	if err != nil {
		return measured, []string{"probe pattern " + probe + " not verified deleted: " + err.Error()}, nil
	}
	for _, p := range all {
		if p.Key == probe {
			return measured, []string{"probe pattern " + probe + " still listed after delete"}, nil
		}
	}
	return measured + "; probe pattern created and deleted", nil, nil
}
