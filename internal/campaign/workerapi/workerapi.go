// Package workerapi holds the wire types shared by the CorrTest web layer and
// the campaign worker, so neither imports the other.
package workerapi

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrLaneNotFound is returned by a worker for an unknown lane id.
var ErrLaneNotFound = errors.New("lane not found")

// ErrLaneExists is returned by Start when the lane id is already running.
var ErrLaneExists = errors.New("lane already exists")

// Info is the worker's version and clock, used by the clock-skew pre-check.
type Info struct {
	Version string
	Now     time.Time
}

// LaneSpec asks a worker to start one lane. Params is lane-kind specific and is
// interpreted by the worker.
type LaneSpec struct {
	ID         string          `json:"id"`
	CampaignID string          `json:"campaignId"`
	Kind       string          `json:"kind"`
	Params     json.RawMessage `json:"params,omitempty"`
}

// LaneStatus is a lane's current state.
type LaneStatus struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	Sent          int       `json:"sent"`
	Failed        int       `json:"failed"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}
