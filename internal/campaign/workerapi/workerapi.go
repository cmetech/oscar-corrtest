// Package workerapi holds the wire types shared by the CorrTest web layer and
// the campaign worker, so neither imports the other.
package workerapi

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// ErrLaneNotFound is returned by a worker for an unknown lane id.
var ErrLaneNotFound = errors.New("lane not found")

// ErrLaneExists is returned by Start when the lane id is already running.
var ErrLaneExists = errors.New("lane already exists")

// Info is the worker's version and clock, used by the clock-skew pre-check.
type Info struct {
	Version string
	Now     time.Time
	// DiskFreeBytes is the free space where the worker runs; 0 means unknown.
	DiskFreeBytes uint64
}

// LaneSpec asks a worker to start one lane. Params is lane-kind specific and is
// interpreted by the worker.
type LaneSpec struct {
	ID         string          `json:"id"`
	CampaignID string          `json:"campaignId"`
	Kind       string          `json:"kind"`
	Params     json.RawMessage `json:"params,omitempty"`
}

// BaselineParams is LaneSpec.Params for Kind "baseline". The lane id is
// LaneSpec.ID (e.g. "<campaignRunID>-baseline") and the plan run id is CampaignID.
type BaselineParams struct {
	TargetURL string                `json:"targetUrl"`
	Seed      int64                 `json:"seed"`
	Baseline  scenario.BaselineSpec `json:"baseline"`
	Duration  time.Duration         `json:"duration"`
	StartAt   time.Time             `json:"startAt,omitempty"` // zero = now
}

// ResolveResult is one resolve-all pass: identities resolved now, identities
// still open after it (failed sends), and the first send error, if any.
type ResolveResult struct {
	Resolved  int    `json:"resolved"`
	Remaining int    `json:"remaining"`
	Error     string `json:"error,omitempty"`
}

// LaneStatus is a lane's current state.
type LaneStatus struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Sent     int    `json:"sent"`
	Failed   int    `json:"failed"`  // definite non-2xx answers
	Unknown  int    `json:"unknown"` // sent, no definite answer (timeout, transport error)
	Missed   int    `json:"missed"`
	Resolved int    `json:"resolved"` // accepted resolves sent by ResolveAll (not planned ones)
	Error    string `json:"error,omitempty"`
	// OrphanStopAt is the orphan_stop marker: set when the watchdog stopped the
	// lane for lack of heartbeats. (The ledger kind column only allows
	// firing/resolve, so the marker lives on the status, not as a ledger row.)
	OrphanStopAt  *time.Time `json:"orphanStopAt,omitempty"`
	LastHeartbeat time.Time  `json:"lastHeartbeat"`
}
