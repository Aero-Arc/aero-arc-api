package domain

import "time"

type OperationalIntentExternalState string

const (
	OperationalIntentExternalStateAccepted  OperationalIntentExternalState = "Accepted"
	OperationalIntentExternalStateActivated OperationalIntentExternalState = "Activated"
	OperationalIntentExternalStateWithdrawn OperationalIntentExternalState = "Withdrawn"
)

type PublicationSyncStatus string

const (
	PublicationSyncPending    PublicationSyncStatus = "pending"
	PublicationSyncProcessing PublicationSyncStatus = "processing"
	PublicationSyncConfirmed  PublicationSyncStatus = "confirmed"
	PublicationSyncRetrying   PublicationSyncStatus = "retrying"
	PublicationSyncBlocked    PublicationSyncStatus = "blocked"
	PublicationSyncFailed     PublicationSyncStatus = "failed"
	PublicationSyncWithdrawn  PublicationSyncStatus = "withdrawn"
)

// OperationalIntentPublication is both the durable DSS publication state and
// the coalescing outbox record for one operational intent.
type OperationalIntentPublication struct {
	IntentID               string                         `json:"intent_id"`
	Revision               int64                          `json:"-"`
	DesiredIntentVersion   int                            `json:"desired_intent_version"`
	PublishedIntentVersion int                            `json:"published_intent_version,omitempty"`
	DesiredState           OperationalIntentExternalState `json:"desired_state"`
	ConfirmedState         OperationalIntentExternalState `json:"confirmed_state,omitempty"`
	SyncStatus             PublicationSyncStatus          `json:"sync_status"`
	DSSVersion             int                            `json:"dss_version,omitempty"`
	OVN                    string                         `json:"ovn,omitempty"`
	SubscriptionID         string                         `json:"subscription_id,omitempty"`
	Manager                string                         `json:"manager,omitempty"`
	USSBaseURL             string                         `json:"uss_base_url,omitempty"`
	ReferenceJSON          []byte                         `json:"reference_json,omitempty"`
	AttemptCount           int                            `json:"attempt_count"`
	NextAttemptAt          time.Time                      `json:"next_attempt_at"`
	LeaseUntil             *time.Time                     `json:"lease_until,omitempty"`
	LastAttemptAt          *time.Time                     `json:"last_attempt_at,omitempty"`
	ConfirmedAt            *time.Time                     `json:"confirmed_at,omitempty"`
	LastError              string                         `json:"last_error,omitempty"`
	UpdatedAt              time.Time                      `json:"updated_at"`
}

type PeerNotification struct {
	ID            string     `json:"id"`
	Revision      int64      `json:"-"`
	IntentID      string     `json:"intent_id"`
	IntentVersion int        `json:"intent_version"`
	USSBaseURL    string     `json:"uss_base_url"`
	Payload       []byte     `json:"payload"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type ReceivedPeerNotification struct {
	ID            string    `json:"id"`
	IntentID      string    `json:"intent_id"`
	Manager       string    `json:"manager"`
	IntentVersion int       `json:"intent_version,omitempty"`
	OVN           string    `json:"ovn,omitempty"`
	Deleted       bool      `json:"deleted"`
	Payload       []byte    `json:"payload"`
	ReceivedAt    time.Time `json:"received_at"`
}
