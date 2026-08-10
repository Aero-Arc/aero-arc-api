package airspaceprovider

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

const (
	ProviderLocal    = "local"
	ProviderInterUSS = "interuss"
)

type Query struct {
	Intent  domain.OperationalIntent
	Volumes []domain.OperationalVolume
}

type Source struct {
	ProviderID  string
	ReferenceID string
	Manager     string
	USSBaseURL  string
	Version     int
	OVN         string
	Local       bool
}

type OperationalIntent struct {
	Source            Source
	Intent            domain.OperationalIntent
	Volumes           []domain.OperationalVolume
	OffNominalVolumes []domain.OperationalVolume
}

// Provider discovers normalized operational intents from one airspace source.
type Provider interface {
	ID() string
	FindOperationalIntents(ctx context.Context, query Query) ([]OperationalIntent, error)
}

type PublicationRequest struct {
	Intent         domain.OperationalIntent
	Volumes        []domain.OperationalVolume
	State          domain.OperationalIntentExternalState
	Key            []string
	OVN            string
	SubscriptionID string
}

type Subscriber struct {
	USSBaseURL    string
	Subscriptions []SubscriptionState
}

type SubscriptionState struct {
	ID                string
	NotificationIndex int
}

type PublicationReceipt struct {
	Manager        string
	Version        int
	OVN            string
	SubscriptionID string
	USSBaseURL     string
	State          domain.OperationalIntentExternalState
	ReferenceJSON  []byte
	Subscribers    []Subscriber
}

// Publisher mutates references owned by this USS in an external coordination
// system. Implementations must treat intent IDs as stable UUIDv4 entity IDs.
type Publisher interface {
	PublicationEnabled() bool
	CreateOperationalIntent(context.Context, PublicationRequest) (PublicationReceipt, error)
	UpdateOperationalIntent(context.Context, PublicationRequest) (PublicationReceipt, error)
	DeleteOperationalIntent(context.Context, string, string) (PublicationReceipt, error)
	GetOperationalIntentReference(context.Context, string) (PublicationReceipt, error)
	BuildPeerNotification(PublicationRequest, PublicationReceipt, Subscriber, bool) ([]byte, error)
	DeliverPeerNotification(context.Context, string, []byte) error
}
