//go:build linux || darwin

package sqlite

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func installedValidationFixture(t *testing.T) (InstalledPackSnapshot, []domain.RunEvent) {
	t.Helper()
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, _, _, spec := installedRun(t, db, true)
	for i := 0; i < 2; i++ {
		e := installedEvidence(t, spec, i)
		if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, spec.StartsAt); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := db.CompleteInstalledPackAttempt(ctx, run.ID, i, e.Request.BodySHA256, installedForwarded(), "", spec.StartsAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, err := db.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := db.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := []domain.RunEvent{}
	for _, e := range all {
		if e.Type == "alert.attempting" {
			events = append(events, e)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return snapshot, events
}
func installedValidationCopy(t *testing.T, s InstalledPackSnapshot, events []domain.RunEvent) (InstalledPackSnapshot, []domain.RunEvent) {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var copy InstalledPackSnapshot
	if err = json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return copy, append([]domain.RunEvent(nil), events...)
}
func TestValidateInstalledPackSnapshotUsesDetachedEvidence(t *testing.T) {
	s, events := installedValidationFixture(t)
	before, beforeEvents := installedValidationCopy(t, s, events)
	m, err := ValidateInstalledPackSnapshot(s, events)
	if err != nil {
		t.Fatal(err)
	}
	if m.Spec() != s.Registration.Spec || !reflect.DeepEqual(s, before) || !reflect.DeepEqual(events, beforeEvents) {
		t.Fatal("validation changed evidence or returned a different manifest")
	}
	if s.Attempts[1].Result != nil || s.Attempts[1].CompletedAt != nil {
		t.Fatal("UNKNOWN was inferred complete")
	}
	s.Attempts = nil
	events = nil
	if _, err = ValidateInstalledPackSnapshot(s, events); err != nil {
		t.Fatal("registered but unattempted is a valid incomplete snapshot:", err)
	}
}
func TestValidateInstalledPackSnapshotRetainsIndeterminateOutcome(t *testing.T) {
	s, events := installedValidationFixture(t)
	at := s.Attempts[1].RecordedAt
	s.Attempts[1].Result = &oscar.InstalledPackInputResult{InjectionResult: oscar.InjectionResult{Class: oscar.InjectionIndeterminate, StatusCode: 503, Body: []byte("unavailable")}, ProfileRevisions: []string{"unexpected", "duplicate"}}
	s.Attempts[1].TransportError = "original bounded transport failure"
	s.Attempts[1].CompletedAt = &at
	if _, err := ValidateInstalledPackSnapshot(s, events); err != nil {
		t.Fatal(err)
	}
}
func TestValidateInstalledPackSnapshotRejectsContradictions(t *testing.T) {
	base, baseEvents := installedValidationFixture(t)
	cases := map[string]func(*InstalledPackSnapshot, *[]domain.RunEvent){
		"fixed registration": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Registration.Kit.RuleName = "foreign" },
		"registration inventory": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Registration.InstallationFiles = s.Registration.InstallationFiles[1:]
		},
		"different token": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Registration.Spec.QualificationRun = strings.Repeat("b", 32)
		},
		"extra attempts": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			for len(s.Attempts) < 16 {
				s.Attempts = append(s.Attempts, s.Attempts[1])
			}
		},
		"reordered attempts": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0], s.Attempts[1] = s.Attempts[1], s.Attempts[0]
		},
		"wrong body": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[1].Evidence.Request.BodyBase64 = "e30="
		},
		"wrong endpoint": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[1].Evidence.Request.Endpoint = "https://foreign.test"
		},
		"wrong expected identity": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[1].Evidence.Expected.Labels["site"] = "foreign"
		},
		"zero recorded time": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Attempts[1].RecordedAt = time.Time{} },
		"completion without result": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			at := s.Attempts[1].RecordedAt
			s.Attempts[1].CompletedAt = &at
		},
		"result without completion": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			r := installedForwarded()
			s.Attempts[1].Result = &r
		},
		"error without result": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Attempts[1].TransportError = "not stored" },
		"zero completion time": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Attempts[0].CompletedAt = &time.Time{} },
		"unresolved predecessor": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0].Result = nil
			s.Attempts[0].CompletedAt = nil
		},
		"uncertain predecessor": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0].Result.Class = oscar.InjectionIndeterminate
		},
		"forwarded wrong status": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Attempts[0].Result.StatusCode = 202 },
		"forwarded wrong body": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0].Result.Body = []byte(`{"status":"failed"}`)
		},
		"forwarded duplicate ACK": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0].Result.ProfileRevisions = append(s.Attempts[0].Result.ProfileRevisions, s.Attempts[0].Result.ProfileRevisions[0])
		},
		"forwarded error": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) { s.Attempts[0].TransportError = "timeout" },
		"oversized body": func(s *InstalledPackSnapshot, _ *[]domain.RunEvent) {
			s.Attempts[0].Result.Body = make([]byte, (1<<20)+1)
		},
		"missing event":       func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { *e = (*e)[:1] },
		"extra event":         func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { *e = append(*e, (*e)[1]) },
		"foreign event run":   func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[1].RunID = "foreign" },
		"wrong event type":    func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[1].Type = "different" },
		"duplicate sequence":  func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[1].Sequence = (*e)[0].Sequence },
		"zero sequence":       func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[0].Sequence = 0 },
		"wrong event request": func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[1].DetailJSON = (*e)[0].DetailJSON },
		"noncanonical event":  func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) { (*e)[1].DetailJSON = " " + (*e)[1].DetailJSON },
		"wrong event time": func(_ *InstalledPackSnapshot, e *[]domain.RunEvent) {
			(*e)[1].OccurredAt = (*e)[1].OccurredAt.Add(time.Second)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, e := installedValidationCopy(t, base, baseEvents)
			mutate(&s, &e)
			m, err := ValidateInstalledPackSnapshot(s, e)
			if err == nil || m != (oscar.InstalledPackManifest{}) {
				t.Fatalf("invalid evidence returned usable manifest: %v", err)
			}
		})
	}
}
