package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Read only allowlisted projections. Never export requests, signatures, target
// configuration, ticket bodies, encrypted intents or credentials.
type SourceReceipt struct {
	History, WriteID, WireEpisode, EventID, Correlation, Locator, Handler, MappingRevision, RefreshDeadline, WireStart, ManagedKey, SourceID, TID string
	Authority, Episode, ActiveSince, Fingerprint, Starts, SourceSeverity, Floor, State                                                            string
	Generation, Variant                                                                                                                           int64
}
type Effect struct {
	Operation, Logical, Kind, Target, TargetState, Callback, CallbackOutcome, SourceWrite, Child, Notifier, Action, ChildState, Severity, ScheduleRoot, Reason, Revision string
	ReceivedAt, EnteredAt, ConfirmedAt                                                                                                                                   string
}
type Binding struct{ ID, Rule, Revision, Notifier, Action, State string }
type Snapshot struct {
	Bindings      []Binding
	Source        []SourceReceipt
	Effects       []Effect
	MeasuredAt    time.Time
	DispatchRoots []string
	QueryDuration time.Duration
}

const sourceSelect = `SELECT JSON_OBJECT('History',h.history_id,'WriteID',h.delivery_id,
 'EventID',h.managed_binding->>'$.record.event_id','WireEpisode',h.managed_binding->>'$.record.identity.episode_id',
 'Locator',h.managed_binding->>'$.record.identity.locator','Handler',h.managed_binding->>'$.record.identity.handler',
 'MappingRevision',h.managed_binding->>'$.record.identity.revision','WireStart',h.managed_binding->>'$.record.identity.started_at',
 'RefreshDeadline',h.native_binding->>'$.transport_ends_at','Correlation',h.managed_binding->>'$.record.identity.correlation','SourceID',h.managed_binding->>'$.record.identity.source_id',
 'TID',h.managed_binding->>'$.record.identity.tid','ManagedKey',n.managed_key,
 'Authority',n.authority_id,'Episode',n.episode_id,'Generation',n.generation,'Variant',v.variant_revision,
 'ActiveSince',n.active_since,'Fingerprint',v.fingerprint,'Starts',v.starts_at,
 'SourceSeverity',v.source_severity,'Floor',n.floor,'State',n.state)
 FROM AM_AlertHistoryWriteReceipt h JOIN AM_NativeActionEpisode n ON n.episode_id=h.native_episode_id
 AND n.generation=h.native_generation AND n.authority_id=h.native_binding->>'$.delivery.episode_ref.authority_id'
 JOIN AM_NativeActionVariant v ON v.episode_row_id=n.id AND v.variant_revision=h.native_binding->>'$.delivery.episode_ref.variant_revision'
 WHERE h.history_id=? AND h.state='complete' ORDER BY h.id LIMIT 257`
const bindingSelect = `SELECT JSON_OBJECT('ID',id,'Rule',selected_rule_id,'Revision',binding_revision_id,'Notifier',notifier_id,'Action',action,'State',binding_state) FROM TM_ResendEpisodeState WHERE history_id=? LIMIT 257`
const dispatchSelect = `SELECT JSON_OBJECT('root',d.root_id) FROM TM_ResendScheduleDispatch d JOIN TM_ResendEpisodeState b ON d.binding_id=b.id WHERE b.history_id=? AND d.id=d.root_id ORDER BY d.id LIMIT 257`
const effectSelect = `SELECT JSON_OBJECT('Operation',o.id,'Logical',o.logical_action_id,'Kind',o.request->>'$.kind',
 'Target',t.target_key,'TargetState',t.state,'Callback',c.delivery_id,'CallbackOutcome',c.outcomes->>'$.state',
 'SourceWrite',c.source->>'$.delivery_id','ReceivedAt',DATE_FORMAT(c.received_at,'%Y-%m-%dT%H:%i:%s.%fZ'),
 'Child',d.id,'Notifier',d.notifier_id,'Action',d.native_effect->>'$.action','ChildState',d.delivery_state,
 'EnteredAt',d.native_effect->>'$.entered_at','ConfirmedAt',DATE_FORMAT(d.native_confirmed_at,'%Y-%m-%dT%H:%i:%s.%fZ'),
 'Severity',COALESCE(d.native_effect->>'$.schedule_severity',c.manifest->>'$.dispatch.effective_severity'),
 'ScheduleRoot',s.root_id,'Reason',s.anchor->>'$.due_reason','Revision',s.command->>'$.policy_binding.revision_id')
 FROM AM_NativeActionOperation o
 JOIN AM_NativeActionTarget t ON t.operation_id=o.id
 LEFT JOIN AM_NativeCallbackReceipt c ON c.delivery_id=t.callback_delivery_id
 LEFT JOIN TM_NotificationDeliveries d ON d.native_callback_id=c.delivery_id
 LEFT JOIN TM_ResendScheduleDispatch s ON s.id=o.id AND s.root_id=o.logical_action_id
 WHERE o.episode_row_id IN (SELECT n.id FROM AM_AlertHistoryWriteReceipt h
 JOIN AM_NativeActionEpisode n ON n.episode_id=h.native_episode_id AND n.generation=h.native_generation
 AND n.authority_id=h.native_binding->>'$.delivery.episode_ref.authority_id' WHERE h.history_id=?)
 ORDER BY c.received_at,t.id,d.id LIMIT 513`

// MySQL uses a dedicated read-only account with an explicitly configured schema
// and verified server identity in its private defaults file. IDs are parameters.
func (c Config) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	var s Snapshot
	u, err := uuid.Parse(id)
	if err != nil || u.String() != id {
		return s, errors.New("invalid owned History ID")
	}
	if !filepath.IsAbs(c.MySQLDefaultsFile) {
		return s, errors.New("absolute private MySQL defaults file required")
	}
	if _, err := privateFile(c.MySQLDefaultsFile); err != nil {
		return s, err
	}
	// PREPARE performs parameter binding; the single UUID variable is populated
	// from hexadecimal UUID bytes. SQL text and identifiers are constants.
	sql := "SET @owned=CONVERT(UNHEX('" + fmt.Sprintf("%x", []byte(u.String())) + "') USING ascii);\nSET TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY; SELECT DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%dT%H:%i:%s.%fZ'); SELECT '---';\n"
	for _, q := range []string{sourceSelect, effectSelect, dispatchSelect, bindingSelect} {
		sql += "PREPARE q FROM '" + strings.ReplaceAll(q, "'", "''") + "'; EXECUTE q USING @owned; DEALLOCATE PREPARE q; SELECT '---';\n"
	}
	sql += "COMMIT;"
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(qctx, "mysql", "--defaults-file="+c.MySQLDefaultsFile, "--ssl-mode=VERIFY_IDENTITY", "--batch", "--raw", "--skip-column-names", "--connect-timeout=3")
	cmd.Stdin = strings.NewReader(sql)
	var out cappedBuffer
	cmd.Stdout = &out
	start := time.Now()
	if cmd.Run() != nil {
		return s, errors.New("owned MySQL receipt read failed")
	}
	s.QueryDuration = time.Since(start)
	sections := strings.Split(out.buf.String(), "---\n")
	if len(sections) != 6 || sections[5] != "" {
		return s, errors.New("incomplete owned receipt read")
	}
	s.MeasuredAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(sections[0]))
	if err != nil {
		return s, errors.New("missing DB clock")
	}
	for _, line := range strings.FieldsFunc(sections[4], func(r rune) bool { return r == '\n' }) {
		var row Binding
		if json.Unmarshal([]byte(line), &row) != nil {
			return s, errors.New("invalid policy binding")
		}
		s.Bindings = append(s.Bindings, row)
	}
	for _, line := range strings.FieldsFunc(sections[3], func(r rune) bool { return r == '\n' }) {
		var row struct{ Root string }
		if json.Unmarshal([]byte(line), &row) != nil || row.Root == "" {
			return s, errors.New("invalid dispatch identity")
		}
		s.DispatchRoots = append(s.DispatchRoots, row.Root)
	}
	for _, line := range strings.FieldsFunc(sections[1], func(r rune) bool { return r == '\n' }) {
		var row SourceReceipt
		if json.Unmarshal([]byte(line), &row) != nil {
			return s, errors.New("invalid source receipt")
		}
		s.Source = append(s.Source, row)
	}
	for _, line := range strings.FieldsFunc(sections[2], func(r rune) bool { return r == '\n' }) {
		var row Effect
		if json.Unmarshal([]byte(line), &row) != nil {
			return s, errors.New("invalid operation receipt")
		}
		s.Effects = append(s.Effects, row)
	}
	if len(s.Bindings) > 256 || len(s.DispatchRoots) > 256 || len(s.Source) > 256 || len(s.Effects) > 512 {
		return s, errors.New("receipt bound exceeded")
	}
	return s, nil
}

type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 1<<20 {
		return 0, errors.New("receipt bound exceeded")
	}
	return b.buf.Write(p)
}
