package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestGuardedPublicationRejectsStaleIntent(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{
		ID: "55555555-5555-4555-8555-555555555555", Version: 1,
		Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	withdrawn := domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version,
		DesiredState: domain.OperationalIntentExternalStateWithdrawn, NextAttemptAt: now, UpdatedAt: now,
	}
	intent.Status = domain.IntentStatusCanceled
	if err := store.UpdateOperationalIntentAndRequestPublication(ctx, intent, 0, withdrawn); err != nil {
		t.Fatal(err)
	}
	activated := withdrawn
	activated.DesiredState = domain.OperationalIntentExternalStateActivated
	if err := store.RequestOperationalIntentPublicationIfCurrent(ctx, activated, 0, domain.IntentStatusAccepted); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("guarded activation error = %v, want version conflict", err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil || publication.DesiredState != domain.OperationalIntentExternalStateWithdrawn {
		t.Fatalf("publication = %#v, %v; want withdrawal preserved", publication, err)
	}
}

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

func TestReplacementPublicationPreservesActiveLease(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	const intentID = "33333333-3333-4333-8333-333333333333"
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: intentID, Version: 1, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	accepted := domain.OperationalIntentPublication{
		IntentID: intentID, DesiredIntentVersion: 1,
		DesiredState: domain.OperationalIntentExternalStateAccepted, NextAttemptAt: now, UpdatedAt: now,
	}
	if err := store.RequestOperationalIntentPublication(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(time.Minute)
	if _, err := store.ClaimOperationalIntentPublication(ctx, intentID, now, leaseUntil); err != nil {
		t.Fatal(err)
	}
	withdrawn := accepted
	withdrawn.DesiredState = domain.OperationalIntentExternalStateWithdrawn
	withdrawn.UpdatedAt = now.Add(time.Second)
	if err := store.RequestOperationalIntentPublication(ctx, withdrawn); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetOperationalIntentPublication(ctx, intentID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LeaseUntil == nil || !stored.LeaseUntil.Equal(leaseUntil) {
		t.Fatalf("replacement lease = %v, want %v", stored.LeaseUntil, leaseUntil)
	}
	if stored.LastAttemptAt == nil || !stored.LastAttemptAt.Equal(now) {
		t.Fatalf("replacement last attempt = %v, want %v", stored.LastAttemptAt, now)
	}
	if claimed, err := store.ClaimDueOperationalIntentPublications(ctx, now.Add(30*time.Second), now.Add(2*time.Minute), 1); err != nil || len(claimed) != 0 {
		t.Fatalf("replacement claimed during active lease: %#v, %v", claimed, err)
	}
	claimed, err := store.ClaimDueOperationalIntentPublications(ctx, leaseUntil, leaseUntil.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 || claimed[0].DesiredState != domain.OperationalIntentExternalStateWithdrawn {
		t.Fatalf("replacement after lease = %#v, %v", claimed, err)
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
