package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestExpiredPublicationLeaseCanBeReclaimed(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	const intentID = "11111111-1111-4111-8111-111111111111"
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: intentID, Version: 1, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RequestOperationalIntentPublication(ctx, domain.OperationalIntentPublication{
		IntentID: intentID, DesiredIntentVersion: 1,
		DesiredState:  domain.OperationalIntentExternalStateAccepted,
		NextAttemptAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueOperationalIntentPublications(ctx, now, now.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	if duplicate, err := store.ClaimDueOperationalIntentPublications(ctx, now.Add(30*time.Second), now.Add(2*time.Minute), 1); err != nil || len(duplicate) != 0 {
		t.Fatalf("claim during lease = %#v, %v", duplicate, err)
	}
	reclaimed, err := store.ClaimDueOperationalIntentPublications(ctx, now.Add(time.Minute), now.Add(2*time.Minute), 1)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("claim after lease = %#v, %v", reclaimed, err)
	}
}

func TestPeerNotificationEnqueueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	const intentID = "22222222-2222-4222-8222-222222222222"
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: intentID, Version: 1, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RequestOperationalIntentPublication(ctx, domain.OperationalIntentPublication{
		IntentID: intentID, DesiredIntentVersion: 1,
		DesiredState:  domain.OperationalIntentExternalStateAccepted,
		NextAttemptAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	publication, err := store.ClaimOperationalIntentPublication(ctx, intentID, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	publication.SyncStatus = domain.PublicationSyncConfirmed
	notification := domain.PeerNotification{
		ID: "notification", IntentID: intentID, IntentVersion: 1,
		USSBaseURL: "https://peer.example", Payload: []byte(`{}`),
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.ConfirmOperationalIntentPublication(ctx, publication, publication.Revision, []domain.PeerNotification{notification}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDuePeerNotifications(ctx, now, now.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	deliveredAt := now.Add(time.Second)
	claimed[0].DeliveredAt = &deliveredAt
	if err := store.UpdatePeerNotification(ctx, claimed[0], claimed[0].Revision); err != nil {
		t.Fatal(err)
	}
	publication, err = store.ClaimOperationalIntentPublication(ctx, intentID, now.Add(2*time.Minute), now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	publication.SyncStatus = domain.PublicationSyncConfirmed
	if err := store.ConfirmOperationalIntentPublication(ctx, publication, publication.Revision, []domain.PeerNotification{notification}); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimDuePeerNotifications(ctx, now.Add(2*time.Minute), now.Add(3*time.Minute), 1)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("duplicate enqueue reset delivered notification: %#v, %v", claimed, err)
	}
}
