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
	Receiver, History, WriteID, WireEpisode, EventID, Correlation, Locator, Handler, MappingRevision, RefreshDeadline, WireStart, ManagedKey, SourceID, TID string
	Authority, Episode, ActiveSince, Fingerprint, Starts, SourceSeverity, Floor, State                                                                      string
	Generation, Variant                                                                                                                                     int64
}
type Effect struct {
	Operation, Logical, Kind, Target, TargetState, Callback, CallbackOutcome, SourceWrite, Child, Notifier, Action, ChildState, Severity, ScheduleRoot, Reason, Revision string
	Receiver, ScopeNotifier, ScopeAction, BindingID, ReceivedAt, EnteredAt, ConfirmedAt                                                                                  string
}
type Binding struct {
	ID, Rule, Revision, Notifier, Action, State string
	CatalogRevision                             int
}
type TicketProof struct {
	Flow, History, Incident, Operation, Audit, Origin, System, AuditSystem, AuditStatus, AuditIncident string
	Episode                                                                                            int
}
type Dispatch struct{ Root, Binding, Revision, Kind, DueAt, State string }
type Snapshot struct {
	Dispatches    []Dispatch
	Tickets       []TicketProof
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
const bindingSelect = `SELECT JSON_OBJECT('ID',b.id,'Rule',b.selected_rule_id,'Revision',b.binding_revision_id,'Notifier',b.notifier_id,'Action',b.action,'State',b.binding_state,'CatalogRevision',v.revision)
 FROM TM_ResendEpisodeState b LEFT JOIN TM_ResendPolicyRevision v ON v.id=b.binding_revision_id AND v.rule_id=b.selected_rule_id AND v.action=b.action AND v.notifier_id=b.notifier_id WHERE b.history_id=? LIMIT 257`
const ticketSelect = `SELECT JSON_OBJECT('Flow',w.flow_id,'History',w.history_id,'Episode',w.episode_number,'Incident',w.incident_number,'Operation',w.provider_operation_id,'Audit',a.id,'Origin',w.origin,'System',w.system,'AuditSystem',a.ticket_system,'AuditStatus',a.status,'AuditIncident',a.incident_number)
 FROM AM_AlertTicketWorkflow w JOIN TKT_Ticketing_Audit a ON a.id=w.ticket_audit_id AND a.ticket_workflow_id=w.flow_id AND a.operation_type='create'
 WHERE w.history_id=? AND w.provider_confirmed_at IS NOT NULL LIMIT 257`
const dispatchSelect = `SELECT JSON_OBJECT('Root',d.root_id,'Binding',d.binding_id,'Revision',d.command->>'$.policy_binding.revision_id','Kind',d.command->>'$.kind','DueAt',DATE_FORMAT(d.due_at,'%Y-%m-%dT%H:%i:%s.%fZ'),'State',d.state) FROM TM_ResendScheduleDispatch d JOIN TM_ResendEpisodeState b ON d.binding_id=b.id WHERE b.history_id=? AND d.id=d.root_id ORDER BY d.id LIMIT 257`
const effectSelect = `SELECT JSON_OBJECT('Operation',o.id,'Logical',o.logical_action_id,'Kind',o.request->>'$.kind',
 'Target',t.target_key,'Receiver',t.configuration->>'$.receiver_id','ScopeNotifier',t.configuration->>'$.scope[0].notifier_id','ScopeAction',t.configuration->>'$.scope[0].action','TargetState',t.state,'Callback',c.delivery_id,'CallbackOutcome',c.outcomes->>'$.state',
 'SourceWrite',c.source->>'$.delivery_id','ReceivedAt',DATE_FORMAT(c.received_at,'%Y-%m-%dT%H:%i:%s.%fZ'),
 'Child',d.id,'Notifier',d.notifier_id,'Action',d.native_effect->>'$.action','ChildState',d.delivery_state,
 'EnteredAt',d.native_effect->>'$.entered_at','ConfirmedAt',DATE_FORMAT(d.native_confirmed_at,'%Y-%m-%dT%H:%i:%s.%fZ'),
 'Severity',COALESCE(d.native_effect->>'$.schedule_severity',c.manifest->>'$.dispatch.effective_severity'),
 'BindingID',s.binding_id,'ScheduleRoot',s.root_id,'Reason',s.anchor->>'$.due_reason','Revision',s.command->>'$.policy_binding.revision_id')
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
	raw, elapsed, err := c.read(ctx, id, []string{sourceSelect, effectSelect, dispatchSelect, bindingSelect, ticketSelect})
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(raw, elapsed)
}

// RootSamples selects only the dispatch identities during sustained timing observation.
func (c Config) RootSamples(ctx context.Context, id string) (Snapshot, error) {
	raw, elapsed, err := c.read(ctx, id, []string{dispatchSelect})
	if err != nil {
		return Snapshot{}, err
	}
	parts := strings.Split(raw, "---\n")
	if len(parts) != 3 || parts[2] != "" {
		return Snapshot{}, errors.New("incomplete dispatch read")
	}
	s := Snapshot{QueryDuration: elapsed}
	s.MeasuredAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(parts[0]))
	if err != nil {
		return s, err
	}
	s.DispatchRoots, s.Dispatches, err = decodeRoots(parts[1])
	return s, err
}
func (c Config) read(ctx context.Context, id string, queries []string, operation ...string) (string, time.Duration, error) {

	u, err := uuid.Parse(id)
	if err != nil || u.String() != id {
		return "", 0, errors.New("invalid owned History ID")
	}
	if !filepath.IsAbs(c.MySQLDefaultsFile) {
		return "", 0, errors.New("absolute private MySQL defaults file required")
	}
	if _, err := privateFile(c.MySQLDefaultsFile); err != nil {
		return "", 0, err
	}
	// Only canonical UUIDs populate bound variables. SQL identifiers are fixed.
	variables := "@owned"
	sql := "SET @owned=CONVERT(UNHEX('" + fmt.Sprintf("%x", []byte(u.String())) + "') USING ascii);\n"
	if len(operation) > 0 {
		op, err := uuid.Parse(operation[0])
		if len(operation) != 1 || err != nil || op.String() != operation[0] {
			return "", 0, errors.New("invalid original operation ID")
		}
		variables += ",@operation"
		sql += "SET @operation=CONVERT(UNHEX('" + fmt.Sprintf("%x", []byte(op.String())) + "') USING ascii);\n"
	}
	sql += "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY; SELECT DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%dT%H:%i:%s.%fZ'); SELECT '---';\n"
	for _, q := range queries {
		sql += "PREPARE q FROM '" + strings.ReplaceAll(q, "'", "''") + "'; EXECUTE q USING " + variables + "; DEALLOCATE PREPARE q; SELECT '---';\n"
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
		return "", 0, errors.New("owned MySQL receipt read failed")
	}
	return out.buf.String(), time.Since(start), nil
}
func decodeSnapshot(raw string, elapsed time.Duration) (Snapshot, error) {
	s := Snapshot{QueryDuration: elapsed}
	var err error
	sections := strings.Split(raw, "---\n")
	if len(sections) != 7 || sections[6] != "" {
		return s, errors.New("incomplete owned receipt read")
	}
	s.MeasuredAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(sections[0]))
	if err != nil {
		return s, errors.New("missing DB clock")
	}
	for _, line := range strings.Split(strings.TrimSpace(sections[5]), "\n") {
		if line == "" {
			continue
		}
		var row TicketProof
		if json.Unmarshal([]byte(line), &row) != nil {
			return s, errors.New("invalid ticket proof")
		}
		s.Tickets = append(s.Tickets, row)
	}
	for _, line := range strings.FieldsFunc(sections[4], func(r rune) bool { return r == '\n' }) {
		var row Binding
		if json.Unmarshal([]byte(line), &row) != nil {
			return s, errors.New("invalid policy binding")
		}
		s.Bindings = append(s.Bindings, row)
	}
	s.DispatchRoots, s.Dispatches, err = decodeRoots(sections[3])
	if err != nil {
		return s, err
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
	if len(s.Tickets) > 256 || len(s.Bindings) > 256 || len(s.DispatchRoots) > 256 || len(s.Source) > 256 || len(s.Effects) > 512 {
		return s, errors.New("receipt bound exceeded")
	}
	return s, nil
}

func decodeRoots(section string) ([]string, []Dispatch, error) {
	var dispatches []Dispatch
	var roots []string
	for _, line := range strings.FieldsFunc(section, func(r rune) bool { return r == '\n' }) {
		var row Dispatch
		if json.Unmarshal([]byte(line), &row) != nil || row.Root == "" || len(roots) >= 256 {
			return nil, nil, errors.New("invalid dispatch identity or bound exceeded")
		}
		dispatches = append(dispatches, row)
		roots = append(roots, row.Root)
	}
	return roots, dispatches, nil
}

type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 1<<20 {
		return 0, errors.New("receipt bound exceeded")
	}
	return b.buf.Write(p)
}

// ResendOperation finds only the issued request within the selected History.
// Recovery may then GET its original signed status without creating an action.
func (c Config) ResendOperation(ctx context.Context, history, request string) (string, error) {
	if _, err := uuid.Parse(request); err != nil {
		return "", err
	}

	raw, _, err := c.read(ctx, history, []string{resendSelect})
	if err != nil {
		return "", err
	}
	parts := strings.Split(raw, "---\n")
	if len(parts) != 3 {
		return "", errors.New("incomplete resend ownership")
	}
	rows := strings.FieldsFunc(parts[1], func(r rune) bool { return r == '\n' })
	if len(rows) > 256 {
		return "", errors.New("resend ownership bound exceeded")
	}
	op := ""
	for _, line := range rows {
		var row struct{ Operation, Request string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return "", errors.New("invalid resend ownership")
		}
		if row.Request == request {
			if op != "" {
				return "", errors.New("ambiguous original request")
			}
			op = row.Operation
		}
	}
	if _, err = uuid.Parse(op); err != nil {
		return "", errors.New("original request not proved")
	}
	return op, nil
}

// ActorFact contains only the auditable owner and terminal provider identifier.
// Read it by exact History, then select the original run-generated request ID.
var ErrActorPending = errors.New("original actor receipt unavailable")

type ActorFact struct {
	Request, History, Kind, Operation, Incident, Audit, Event, Outcome string
	Episode                                                            int
}

func (c Config) ActorFact(ctx context.Context, history, request, kind string) (ActorFact, error) {

	var out ActorFact
	raw, _, err := c.read(ctx, history, []string{actorSelect})
	if err != nil {
		return out, err
	}
	sections := strings.Split(raw, "---\n")
	if len(sections) != 3 {
		return out, errors.New("incomplete original actor read")
	}
	lines := strings.FieldsFunc(sections[1], func(r rune) bool { return r == '\n' })
	if len(lines) > 256 {
		return out, errors.New("actor evidence bound exceeded")
	}
	for _, line := range lines {
		var r ActorFact
		if json.Unmarshal([]byte(line), &r) != nil {
			return out, errors.New("invalid actor evidence")
		}
		if r.History != history || r.Request != request {
			continue
		}
		if kind == "acknowledge" && (r.Kind != "alert_acknowledged" || r.Outcome != "succeeded") {
			continue
		}
		if kind == "manual_ticket" && (r.Kind != "action_requested" || r.Incident == "" || r.Audit == "") {
			continue
		}
		if out.Event != "" {
			return out, errors.New("ambiguous original actor receipt")
		}
		out = r
	}
	if out.Event == "" || out.Operation == "" || out.Episode < 1 {
		return out, ErrActorPending
	}
	return out, nil
}

const resendSelect = "SELECT JSON_OBJECT('Operation',ID,'Request',request_id) FROM AM_AlertResend WHERE original_alert_id=? LIMIT 257"
const actorSelect = `SELECT JSON_OBJECT('History',e.original_alert_id,'Episode',e.episode_number,'Request',e.request_id,'Operation',e.operation_id,'Event',e.ID,'Kind',e.event_type,'Outcome',e.outcome,'Incident',a.incident_number,'Audit',a.id)
 FROM AM_AlertAudit e LEFT JOIN TKT_Ticketing_Audit a ON a.alert_id=e.original_alert_id AND JSON_VALID(a.status_details)
 AND JSON_UNQUOTE(JSON_EXTRACT(IF(JSON_VALID(a.status_details),a.status_details,'{}'),'$.manual_alert_operation'))=e.operation_id
 AND JSON_UNQUOTE(JSON_EXTRACT(IF(JSON_VALID(a.status_details),a.status_details,'{}'),'$.request_id'))=e.request_id
 AND a.status='success' AND a.ticket_system='snow' AND a.operation_type='create'
 WHERE e.original_alert_id=? AND e.source='alert_view' AND e.actor_type='user' AND (e.event_type='alert_acknowledged' OR (e.event_type='action_requested' AND e.reason_code='ticket_create')) LIMIT 257`

func (c Config) CheckAccess(ctx context.Context) error {
	_, _, err := c.read(ctx, uuid.Nil.String(), []string{sourceSelect, effectSelect, dispatchSelect, bindingSelect, ticketSelect, actorSelect, resendSelect})
	if err == nil {
		_, err = c.Original(ctx, uuid.Nil.String(), uuid.Nil.String())
	}
	return err
}
