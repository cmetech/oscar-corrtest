package managed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// One projected row per expected frozen child. LEFT joins retain an unreserved
// operation, an absent callback, and a manifest child with no reported effect.
type OriginalChild struct {
	History, Root, Operation, Parent, OpState, Kind                              string
	Episode                                                                      int
	Target, TargetState, Successor, Callback, Aggregate                          string
	PreEntryNoEffect, ManifestOwned, ProviderNoEffect                            int
	Expected, Reported, Held                                                     int
	Name, Notifier, Action, PolicyReceipt, Child, ReceiptType, State             string
	ProviderOutcome, ProviderConfirmed, ProviderPartial, ProviderAction, Attempt string
}

type OriginalReceipt struct {
	Children      []OriginalChild
	QueryDuration time.Duration
}

const originalSelect = `WITH owned AS (
 SELECT DISTINCT h.history_id,h.episode_number,n.id AS episode_row
 FROM AM_AlertHistoryWriteReceipt h JOIN AM_NativeActionEpisode n
 ON n.episode_id=h.native_episode_id AND n.generation=h.native_generation
 AND n.authority_id=h.native_binding->>'$.delivery.episode_ref.authority_id'
 WHERE h.history_id=? AND h.state='complete'
), original AS (
 SELECT owned.*,seed.logical_action_id AS root FROM owned JOIN AM_NativeActionOperation seed
 ON seed.episode_row_id=owned.episode_row WHERE seed.id=?
), physical AS (
 SELECT r.history_id,r.episode_number,r.episode_row,r.root,o.id AS op,o.state AS opstate,
 o.supersedes_operation_id AS parent,o.request->>'$.kind' AS kind,t.target_key,t.state AS tstate,
 t.successor_id,t.callback_delivery_id AS cb,
 IF(t.state='no_effect' AND t.entry_digest IS NULL AND t.callback_entry IS NULL AND
 t.evidence IN ('ENTRY_NOT_CLAIMED','DESTINATION_REMOVED','PERMIT_INVALIDATED_BEFORE_ENTRY','NATIVE_PERMIT_EXPIRED','NATIVE_EPISODE_RESOLVED','NATIVE_EPISODE_MISMATCH'),1,0) AS preentry,
 c.manifest->'$.dispatch' AS manifest,c.outcomes
 FROM original r JOIN AM_NativeActionOperation o ON o.logical_action_id=r.root AND o.episode_row_id=r.episode_row
 LEFT JOIN AM_NativeActionTarget t ON t.operation_id=o.id
 LEFT JOIN AM_NativeCallbackReceipt c ON c.delivery_id=t.callback_delivery_id
), children AS (
 SELECT p.*,k.name,
 JSON_EXTRACT(p.manifest,CONCAT('$.targets.',JSON_QUOTE(k.name))) AS candidate,
 JSON_EXTRACT(p.manifest,CONCAT('$.policy_targets.',JSON_QUOTE(k.name),'.receipt_id')) AS policy_id,
 JSON_EXTRACT(p.outcomes,CONCAT('$.effects.',JSON_QUOTE(k.name))) AS effect
 FROM physical p LEFT JOIN JSON_TABLE(COALESCE(JSON_KEYS(p.manifest,'$.targets'),JSON_ARRAY()),
 '$[*]' COLUMNS(name VARCHAR(512) PATH '$' ERROR ON ERROR)) k ON TRUE
), evidence AS (
 SELECT k.*,
 CASE WHEN effect->>'$.provider.owner'='original_policy_receipt' AND effect->>'$.receipt_type'='NotificationDelivery'
 THEN d.native_effect ELSE effect->'$.provider' END AS provider
 FROM children k LEFT JOIN TM_NotificationDeliveries d ON d.id=JSON_UNQUOTE(k.policy_id)
 AND d.id=k.effect->>'$.id' AND d.target=k.name AND d.notifier_id=k.candidate->>'$.notifier_id'
 AND d.policy_kind='resend' AND d.delivery_state=k.effect->>'$.state'
)
SELECT JSON_OBJECT('History',history_id,'Episode',episode_number,'Root',root,'Operation',op,
 'Parent',parent,'OpState',opstate,'Kind',kind,'Target',target_key,'TargetState',tstate,'Successor',successor_id,
 'Callback',cb,'Aggregate',outcomes->>'$.state','PreEntryNoEffect',preentry,
 'ManifestOwned',IF(manifest->>'$.id'=cb AND manifest->>'$.episode_row_id'=episode_row
 AND manifest->>'$.source.history_id'=history_id AND manifest->>'$.source.episode_number'=episode_number
 AND (manifest->>'$.root'=root OR (kind='ordinary' AND JSON_TYPE(manifest->'$.root')='NULL'))
 AND JSON_TYPE(manifest->'$.targets')='OBJECT' AND JSON_TYPE(manifest->'$.held')='OBJECT'
 AND JSON_TYPE(outcomes->'$.effects')='OBJECT',1,0),
 'Expected',JSON_LENGTH(manifest,'$.targets'),'Held',JSON_LENGTH(manifest,'$.held'),'Reported',JSON_LENGTH(outcomes,'$.effects'),
 'Name',name,'Notifier',candidate->>'$.notifier_id','Action',candidate->>'$.action','PolicyReceipt',JSON_UNQUOTE(policy_id),
 'Child',effect->>'$.id','ReceiptType',effect->>'$.receipt_type','State',effect->>'$.state',
 'ProviderAction',provider->>'$.action','ProviderOutcome',provider->>'$.outcome','ProviderConfirmed',provider->>'$.confirmed_at',
 'ProviderPartial',provider->>'$.evidence.partial','Attempt',provider->>'$.attempt_id',
 'ProviderNoEffect',IF(provider->>'$.evidence.flow_status' IN ('completed','failed','cancelled','expired')
 AND provider->>'$.evidence.primary.performed'='false' AND provider->>'$.evidence.primary.state' IN ('skipped','failed')
 AND JSON_TYPE(provider->'$.evidence.operations')='ARRAY' AND JSON_LENGTH(provider,'$.evidence.operations')<=64
 AND NOT EXISTS (SELECT 1 FROM JSON_TABLE(COALESCE(provider->'$.evidence.operations',JSON_ARRAY()),'$[*]'
 COLUMNS(receipt VARCHAR(32) PATH '$.receipt', outcome VARCHAR(32) PATH '$.outcome')) a
 WHERE NOT COALESCE((a.receipt='rejected' AND a.outcome='failed') OR
 (a.receipt IN ('accepted','rejected') AND a.outcome='no_effect'),0)),1,0))
 FROM evidence ORDER BY op,target_key,name LIMIT 513`

func (c Config) Original(ctx context.Context, history, operation string) (OriginalReceipt, error) {
	raw, elapsed, err := c.read(ctx, history, []string{originalSelect}, operation)
	if err != nil {
		return OriginalReceipt{}, err
	}
	return decodeOriginal(raw, elapsed)
}
func decodeOriginal(raw string, elapsed time.Duration) (OriginalReceipt, error) {
	out := OriginalReceipt{QueryDuration: elapsed}
	parts := strings.Split(raw, "---\n")
	if len(parts) != 3 || parts[2] != "" {
		return out, errors.New("incomplete original receipt read")
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(parts[0])); err != nil {
		return out, err
	}
	for _, line := range strings.FieldsFunc(parts[1], func(r rune) bool { return r == '\n' }) {
		var row OriginalChild
		if len(out.Children) >= 512 || json.Unmarshal([]byte(line), &row) != nil {
			return OriginalReceipt{}, errors.New("invalid or oversized original child projection")
		}
		out.Children = append(out.Children, row)
	}
	return out, nil
}

func (r OriginalChild) expectedID() string {
	if r.PolicyReceipt != "" {
		return r.PolicyReceipt
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("oscar-history-delivery:"+r.Callback+":"+r.Name)).String()
}

// The collector reports a compact failed attempt only for definite_no_effect.
// A bare failed state on a live delivery cannot distinguish provider entry.
func (r OriginalChild) settled() bool {
	if r.Name == "" || r.Notifier == "" || (r.Action != "notification" && r.Action != "ticket") || r.Child != r.expectedID() || (r.ProviderPartial != "" && r.ProviderPartial != "false") {
		return false
	}
	if r.ReceiptType == "ResendReceiptKey" {
		_, err := uuid.Parse(r.Attempt)
		return r.PolicyReceipt == r.Child && err == nil && (r.State == "sent" || r.State == "failed")
	}
	if r.ReceiptType != "NotificationDelivery" || r.ProviderAction != r.Action {
		return false
	}
	switch r.State {
	case "sent":
		_, err := time.Parse(time.RFC3339Nano, r.ProviderConfirmed)
		if err != nil {
			_, err = time.Parse(time.RFC3339Nano, r.ProviderConfirmed+"Z")
		}
		return r.ProviderOutcome == "sent" && err == nil
	case "failed":
		return r.ProviderOutcome == "failed" && r.ProviderConfirmed == "" && r.ProviderNoEffect == 1
	case "suppressed":
		return r.ProviderOutcome == "suppressed" && r.ProviderConfirmed == ""
	default:
		return false
	}
}
