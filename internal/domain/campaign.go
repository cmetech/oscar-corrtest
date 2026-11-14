package domain

import (
	"encoding/json"
	"time"
)

type CampaignStatus string

const (
	CampaignPrecheck    CampaignStatus = "PRECHECK"
	CampaignRunning     CampaignStatus = "RUNNING"
	CampaignStopping    CampaignStatus = "STOPPING"
	CampaignDraining    CampaignStatus = "DRAINING"
	CampaignCleaningUp  CampaignStatus = "CLEANING_UP"
	CampaignCompleted   CampaignStatus = "COMPLETED"
	CampaignInterrupted CampaignStatus = "INTERRUPTED"
	CampaignRecovering  CampaignStatus = "RECOVERING"
)

// Campaign is one alerting load campaign. Trigger is cli|ui|recovery; RunBy is
// agent|human (UI is always human). Zero EndedAt means not ended.
type Campaign struct {
	ID, ScenarioID, ScenarioHash string
	Custom                       bool
	Trigger, RunBy               string
	TriggerIdentity              string
	Status                       CampaignStatus
	Verdict                      Verdict
	Cleanup                      CleanupStatus
	Comparable                   bool
	StartedAt, EndedAt           time.Time
	Scorecard, Report            json.RawMessage
}

type CampaignFilter struct {
	Status CampaignStatus // empty = any
	Limit  int            // 0 = no limit
}

type PrecheckResult struct {
	Passed bool
	Detail json.RawMessage
	At     time.Time
}

// CampaignMutation is an intent-log entry for a change the campaign made to the
// target; open until RestoredAt is set. Kind: notifier_disable|policy_create|
// policy_activate|sink_enable|incident_close.
type CampaignMutation struct {
	ID                     int64
	CampaignID, Kind       string
	ObjectID               string
	Prior                  json.RawMessage
	RecordedAt, RestoredAt time.Time
}

// LaneRow is one lane-ledger entry; Done is false until the outcome is recorded.
// CampaignRunLink ties a correlation run to a campaign.
type CampaignRunLink struct {
	RunID, Kind string
}

type LaneRow struct {
	LaneID              string
	Seq                 int
	Kind, Identity      string
	Severity, Instance  string
	Ticket              bool
	ScheduledAt, SentAt time.Time
	Done, Missed        bool
	Status              int
	ErrorClass          string
	LatencyMillis       int64
}

var campaignTransitions = map[CampaignStatus][]CampaignStatus{
	CampaignPrecheck:    {CampaignRunning, CampaignCompleted, CampaignInterrupted},
	CampaignRunning:     {CampaignStopping, CampaignDraining, CampaignInterrupted},
	CampaignStopping:    {CampaignDraining, CampaignInterrupted},
	CampaignDraining:    {CampaignCleaningUp, CampaignInterrupted},
	CampaignCleaningUp:  {CampaignCompleted, CampaignInterrupted},
	CampaignInterrupted: {CampaignRecovering},
	CampaignRecovering:  {CampaignCleaningUp, CampaignCompleted, CampaignInterrupted},
}

func CanTransitionCampaign(from, to CampaignStatus) bool {
	for _, n := range campaignTransitions[from] {
		if n == to {
			return true
		}
	}
	return false
}

// CampaignEvent is one append-only campaign timeline entry.
type CampaignEvent struct {
	CampaignID string
	Sequence   int
	Type       string
	Level      string
	OccurredAt time.Time
	Summary    string
	Detail     json.RawMessage
}
