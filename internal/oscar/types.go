// Package oscar implements versioned public HTTP adapters without leaking wire types.
package oscar

import "time"

type Rule struct {
	ID          int
	Name        string
	Pattern     string
	Description string
}

type InjectionClass string

const (
	InjectionAccepted      InjectionClass = "accepted"
	InjectionRejected      InjectionClass = "rejected"
	InjectionQueued        InjectionClass = "queued"
	InjectionPartial       InjectionClass = "partial"
	InjectionIndeterminate InjectionClass = "indeterminate"
)

type InjectionResult struct {
	Class      InjectionClass `json:"class"`
	StatusCode int            `json:"statusCode"`
	TaskID     string         `json:"taskId,omitempty"`
	Body       []byte         `json:"body,omitempty"`
}

type HistoryQuery struct {
	AlertName string
	Start     time.Time
	End       time.Time
}

type HistoryRecord struct {
	ID          string            `json:"id"`
	AlertName   string            `json:"alertName"`
	Fingerprint string            `json:"fingerprint"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type AuditRecord struct {
	ID                  int               `json:"id"`
	CreatedAt           time.Time         `json:"createdAt"`
	AlertFingerprint    string            `json:"alertFingerprint"`
	RuleID              int               `json:"ruleId"`
	RuleName            string            `json:"ruleName"`
	Pattern             string            `json:"pattern"`
	Outcome             string            `json:"outcome"`
	SuppressedNotifiers []string          `json:"suppressedNotifiers,omitempty"`
	ParentFingerprint   string            `json:"parentFingerprint,omitempty"`
	AddedLabels         map[string]string `json:"addedLabels,omitempty"`
	Reason              string            `json:"reason,omitempty"`
}

type NotificationRecord struct {
	ID               string            `json:"id"`
	AlertFingerprint string            `json:"alertFingerprint"`
	NotifierType     string            `json:"notifierType"`
	Status           string            `json:"status"`
	CreatedAt        time.Time         `json:"createdAt"`
	Labels           map[string]string `json:"labels,omitempty"`
}

// MachineError preserves safe HTTP failure metadata without credentials.
type MachineError struct {
	Operation  string
	StatusCode int
	Code       string
	Detail     string
}

func (e *MachineError) Error() string {
	if e.StatusCode != 0 {
		return e.Operation + " failed with HTTP status " + itoa(e.StatusCode) + ": " + e.Detail
	}
	return e.Operation + " failed: " + e.Detail
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
