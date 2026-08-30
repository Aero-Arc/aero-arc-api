//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestAuthoritativeSpatialReadCheckSlice(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	observer, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	if _, err := store.pool.Exec(ctx, `TRUNCATE mission_deployments, mission_items, missions, flight_records, aircraft, received_peer_notifications, peer_notifications, operational_intent_publications, conflict_findings, operational_volumes, operational_intents`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	target := integrationVolume("target-volume", "target", 1, now,
		`{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}}`)
	overlap := integrationVolume("overlap-volume", "overlap", 1, now,
		`{"type":"Polygon","coordinates":[[[-96.5,32.5],[-95.5,32.5],[-95.5,33.5],[-96.5,33.5],[-96.5,32.5]]]}`)
	distant := integrationVolume("distant-volume", "distant", 1, now,
		`{"type":"Polygon","coordinates":[[[-80,20],[-79,20],[-79,21],[-80,21],[-80,20]]]}`)
	for _, id := range []string{target.IntentID, overlap.IntentID, distant.IntentID} {
		if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
			ID: id, Version: 1, AircraftID: id + "-aircraft", PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, volume := range []domain.OperationalVolume{target, overlap, distant} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatal(err)
		}
	}

	// Query through a second store instance to model another API replica.
	candidates, err := observer.FindCandidates(ctx, durable.CandidateQuery{
		ExcludeIntentID: target.IntentID,
		Volumes:         []domain.OperationalVolume{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].IntentID != overlap.IntentID {
		t.Fatalf("candidates = %#v", candidates)
	}

	replacement := distant
	replacement.ID = "overlap-moved"
	replacement.IntentID = overlap.IntentID
	if err := store.ReplaceOperationalVolumes(ctx, overlap.IntentID, 1, []domain.OperationalVolume{replacement}); err != nil {
		t.Fatal(err)
	}
	candidates, err = observer.FindCandidates(ctx, durable.CandidateQuery{
		ExcludeIntentID: target.IntentID,
		Volumes:         []domain.OperationalVolume{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after replacement = %#v", candidates)
	}

	invalid := target
	invalid.ID = "invalid"
	invalid.EndsAt = invalid.StartsAt
	if err := store.RecordOperationalVolume(ctx, invalid); err == nil {
		t.Fatal("expected PostGIS to reject invalid time window")
	}
}

func TestConcurrentIntentUpdatesUseOptimisticRevision(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("concurrent-intent", now)
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	one, err := first.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	one.Status = domain.IntentStatusSubmitted
	one.UpdatedAt = now.Add(time.Second)
	two.Status = domain.IntentStatusCanceled
	two.UpdatedAt = now.Add(2 * time.Second)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, update := range []struct {
		store  *Store
		intent domain.OperationalIntent
	}{{first, one}, {second, two}} {
		go func() {
			ready.Done()
			<-start
			errs <- update.store.UpdateOperationalIntent(ctx, update.intent, update.intent.Revision)
		}()
	}
	ready.Wait()
	close(start)
	var succeeded, conflicted int
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, durable.ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent update error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("successful updates = %d, conflicts = %d", succeeded, conflicted)
	}
	stored, err := first.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 1 {
		t.Fatalf("stored revision = %d, want 1", stored.Revision)
	}
}

func TestConcurrentAircraftActivationsAllowExactlyOneIntent(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	intents := []domain.OperationalIntent{
		integrationIntent("activation-a", now),
		integrationIntent("activation-b", now),
	}
	for index := range intents {
		intents[index].AircraftID = "aircraft-race"
		intents[index].Status = domain.IntentStatusAccepted
		if err := first.CreateOperationalIntent(ctx, intents[index]); err != nil {
			t.Fatal(err)
		}
	}
	stores := []*Store{first, second}
	start := make(chan struct{})
	errs := make(chan error, len(intents))
	var ready sync.WaitGroup
	ready.Add(len(intents))
	for index, accepted := range intents {
		active := accepted
		active.Status = domain.IntentStatusActive
		store := stores[index]
		go func() {
			ready.Done()
			<-start
			errs <- store.ActivateOperationalIntent(ctx, active, active.Revision)
		}()
	}
	ready.Wait()
	close(start)

	var activated, rejected int
	for range intents {
		switch err := <-errs; {
		case err == nil:
			activated++
		case errors.Is(err, durable.ErrActiveIntent):
			rejected++
		default:
			t.Fatalf("activation error = %v", err)
		}
	}
	if activated != 1 || rejected != 1 {
		t.Fatalf("activated = %d, rejected = %d", activated, rejected)
	}
	stored, err := first.ListOperationalIntents(ctx, "aircraft-race")
	if err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, intent := range stored {
		if intent.Status == domain.IntentStatusActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active intents = %d, want 1; intents=%#v", activeCount, stored)
	}
}

func TestUpdateOperationalIntentRejectsSupersededVersion(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	v1 := integrationIntent("superseded-intent", now)
	v1.Status = domain.IntentStatusAccepted
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := store.ReplaceOperationalIntent(ctx, v1.Version, v1.Revision, v2, nil); err != nil {
		t.Fatal(err)
	}

	stale := v1
	stale.Status = domain.IntentStatusActive
	stale.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateOperationalIntent(ctx, stale, v1.Revision); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded update error = %v, want ErrVersionConflict", err)
	}
	stored, err := store.GetOperationalIntentVersion(ctx, v1.ID, v1.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.IntentStatusAccepted || stored.Revision != 0 {
		t.Fatalf("superseded version changed: %#v", stored)
	}
}

func TestTerminalUpdateRetiresPriorAcceptedVersion(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	v1 := integrationIntent("terminal-versioned-intent", now)
	v1.Status = domain.IntentStatusAccepted
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := store.ReplaceOperationalIntent(ctx, 1, 0, v2, nil); err != nil {
		t.Fatal(err)
	}
	canceledAt := now.Add(time.Minute)
	v2.Status = domain.IntentStatusCanceled
	v2.CanceledAt = &canceledAt
	v2.UpdatedAt = canceledAt
	if err := store.UpdateOperationalIntent(ctx, v2, 0); err != nil {
		t.Fatal(err)
	}
	storedV1, err := store.GetOperationalIntentVersion(ctx, v1.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if storedV1.Status != domain.IntentStatusCanceled || storedV1.CanceledAt == nil || !storedV1.CanceledAt.Equal(canceledAt) {
		t.Fatalf("stored v1 = %#v, want canceled at %v", storedV1, canceledAt)
	}
}

func TestAcceptOperationalIntentSupersedesPriorAcceptedVersion(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	v1 := integrationIntent("accepted-replacement-intent", now)
	v1.Status = domain.IntentStatusAccepted
	v1.AcceptedAt = &now
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusSubmitted
	v2.AcceptedAt = nil
	if err := store.ReplaceOperationalIntent(ctx, v1.Version, v1.Revision, v2, nil); err != nil {
		t.Fatal(err)
	}
	acceptedAt := now.Add(time.Minute)
	v2.Status = domain.IntentStatusAccepted
	v2.AcceptedAt = &acceptedAt
	v2.UpdatedAt = acceptedAt
	if err := store.AcceptOperationalIntent(ctx, v2, v2.Revision); err != nil {
		t.Fatal(err)
	}

	storedV1, err := store.GetOperationalIntentVersion(ctx, v1.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if storedV1.Status != domain.IntentStatusSuperseded || storedV1.SupersededAt == nil || !storedV1.SupersededAt.Equal(acceptedAt) {
		t.Fatalf("stored v1 = %#v, want superseded at %v", storedV1, acceptedAt)
	}
	storedV2, err := store.GetOperationalIntentVersion(ctx, v2.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if storedV2.Status != domain.IntentStatusAccepted || storedV2.Revision != 1 {
		t.Fatalf("stored v2 = %#v, want accepted revision 1", storedV2)
	}
}

func TestPublicationRequestIsAtomicAndLeasedAcrossReplicas(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("11111111-1111-4111-8111-111111111111", now)
	intent.Status = domain.IntentStatusSubmitted
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	acceptedAt := now.Add(time.Minute)
	intent.Status = domain.IntentStatusAccepted
	intent.AcceptedAt = &acceptedAt
	intent.UpdatedAt = acceptedAt
	publication := domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: 1,
		DesiredState:  domain.OperationalIntentExternalStateAccepted,
		NextAttemptAt: acceptedAt, UpdatedAt: acceptedAt,
	}
	if err := first.AcceptOperationalIntentAndRequestPublication(ctx, intent, 0, publication); err != nil {
		t.Fatal(err)
	}
	storedIntent, err := second.GetOperationalIntent(ctx, intent.ID)
	if err != nil || storedIntent.Status != domain.IntentStatusAccepted {
		t.Fatalf("accepted intent = %#v, %v", storedIntent, err)
	}
	claimed, err := first.ClaimDueOperationalIntentPublications(ctx, acceptedAt, acceptedAt.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	claimedAgain, err := second.ClaimDueOperationalIntentPublications(ctx, acceptedAt, acceptedAt.Add(time.Minute), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("second replica also claimed leased publication: %#v", claimedAgain)
	}
	claimed, err = second.ClaimDueOperationalIntentPublications(ctx, acceptedAt.Add(time.Minute), acceptedAt.Add(2*time.Minute), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("expired publication lease was not reclaimed: %#v, %v", claimed, err)
	}
	notification := domain.PeerNotification{
		ID: "notification-1", IntentID: intent.ID, IntentVersion: 1,
		USSBaseURL: "https://peer.example", Payload: []byte(`{"operational_intent_id":"test"}`),
		NextAttemptAt: acceptedAt, CreatedAt: acceptedAt, UpdatedAt: acceptedAt,
	}
	claimed[0].SyncStatus = domain.PublicationSyncConfirmed
	claimed[0].ConfirmedState = domain.OperationalIntentExternalStateAccepted
	claimed[0].PublishedIntentVersion = 1
	if err := first.ConfirmOperationalIntentPublication(ctx, claimed[0], claimed[0].Revision, []domain.PeerNotification{notification}); err != nil {
		t.Fatal(err)
	}
	notifications, err := second.ClaimDuePeerNotifications(ctx, acceptedAt, acceptedAt.Add(time.Minute), 1)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("notification claim = %#v, %v", notifications, err)
	}
	if duplicate, err := first.ClaimDuePeerNotifications(ctx, acceptedAt, acceptedAt.Add(time.Minute), 1); err != nil || len(duplicate) != 0 {
		t.Fatalf("duplicate notification claim = %#v, %v", duplicate, err)
	}
	deliveredAt := acceptedAt.Add(time.Second)
	notifications[0].DeliveredAt = &deliveredAt
	if err := second.UpdatePeerNotification(ctx, notifications[0], notifications[0].Revision); err != nil {
		t.Fatal(err)
	}
	received := domain.ReceivedPeerNotification{
		ID: "received-1", IntentID: intent.ID, Manager: "peer-uss",
		IntentVersion: 2, OVN: "peer-ovn", Payload: []byte(`{"operational_intent_id":"test"}`),
		ReceivedAt: deliveredAt,
	}
	if err := first.RecordReceivedPeerNotification(ctx, received); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordReceivedPeerNotification(ctx, received); err != nil {
		t.Fatal(err)
	}
	receivedNotifications, err := second.ListReceivedPeerNotifications(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receivedNotifications) != 1 || receivedNotifications[0].OVN != "peer-ovn" {
		t.Fatalf("received notifications = %#v", receivedNotifications)
	}
}

func TestGuardedPublicationRejectsStaleIntentAcrossReplicas(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("55555555-5555-4555-8555-555555555555", now)
	intent.Status = domain.IntentStatusAccepted
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	withdrawn := domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version,
		DesiredState: domain.OperationalIntentExternalStateWithdrawn, NextAttemptAt: now, UpdatedAt: now,
	}
	intent.Status = domain.IntentStatusCanceled
	if err := second.UpdateOperationalIntentAndRequestPublication(ctx, intent, 0, withdrawn); err != nil {
		t.Fatal(err)
	}
	activated := withdrawn
	activated.DesiredState = domain.OperationalIntentExternalStateActivated
	if err := first.RequestOperationalIntentPublicationIfCurrent(ctx, activated, intent.Version, 0, domain.IntentStatusAccepted); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("guarded activation error = %v, want version conflict", err)
	}
	publication, err := first.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil || publication.DesiredState != domain.OperationalIntentExternalStateWithdrawn {
		t.Fatalf("publication = %#v, %v; want withdrawal preserved", publication, err)
	}
}

func TestGuardedPublicationCanRestorePriorAcceptedVersionAcrossReplicas(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	v1 := integrationIntent("66666666-6666-4666-8666-666666666666", now)
	v1.Status = domain.IntentStatusAccepted
	if err := first.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	publication := domain.OperationalIntentPublication{
		IntentID: v1.ID, DesiredIntentVersion: 1,
		DesiredState: domain.OperationalIntentExternalStateActivated, NextAttemptAt: now, UpdatedAt: now,
	}
	if err := first.RequestOperationalIntentPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := second.ReplaceOperationalIntent(ctx, 1, 0, v2, nil); err != nil {
		t.Fatal(err)
	}
	publication.DesiredState = domain.OperationalIntentExternalStateAccepted
	if err := first.RequestOperationalIntentPublicationIfCurrent(ctx, publication, 2, 0, domain.IntentStatusDraft); err != nil {
		t.Fatal(err)
	}
	stored, err := second.GetOperationalIntentPublication(ctx, v1.ID)
	if err != nil || stored.DesiredIntentVersion != 1 || stored.DesiredState != domain.OperationalIntentExternalStateAccepted {
		t.Fatalf("publication = %#v, %v; want accepted v1", stored, err)
	}
}

func TestReplacementPublicationPreservesLeaseAcrossReplicas(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("44444444-4444-4444-8444-444444444444", now)
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	accepted := domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version,
		DesiredState: domain.OperationalIntentExternalStateAccepted, NextAttemptAt: now, UpdatedAt: now,
	}
	if err := first.RequestOperationalIntentPublication(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(time.Minute)
	claimedPublication, err := first.ClaimOperationalIntentPublication(ctx, intent.ID, now, leaseUntil)
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil = now.Add(2 * time.Minute)
	if err := first.RenewOperationalIntentPublicationLease(ctx, intent.ID, claimedPublication.Revision, leaseUntil); err != nil {
		t.Fatal(err)
	}
	withdrawn := accepted
	withdrawn.DesiredState = domain.OperationalIntentExternalStateWithdrawn
	withdrawn.UpdatedAt = now.Add(time.Second)
	if err := second.RequestOperationalIntentPublication(ctx, withdrawn); err != nil {
		t.Fatal(err)
	}
	if err := first.RenewOperationalIntentPublicationLease(ctx, intent.ID, claimedPublication.Revision, leaseUntil.Add(time.Minute)); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale renewal error = %v, want version conflict", err)
	}
	stored, err := first.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LeaseUntil == nil || !stored.LeaseUntil.Equal(leaseUntil) {
		t.Fatalf("replacement lease = %v, want %v", stored.LeaseUntil, leaseUntil)
	}
	if stored.LastAttemptAt == nil || !stored.LastAttemptAt.Equal(now) {
		t.Fatalf("replacement last attempt = %v, want %v", stored.LastAttemptAt, now)
	}
	if claimed, err := first.ClaimDueOperationalIntentPublications(ctx, now.Add(30*time.Second), now.Add(2*time.Minute), 1); err != nil || len(claimed) != 0 {
		t.Fatalf("replacement claimed during active lease: %#v, %v", claimed, err)
	}
	claimed, err := second.ClaimDueOperationalIntentPublications(ctx, leaseUntil, leaseUntil.Add(time.Minute), 1)
	if err != nil || len(claimed) != 1 || claimed[0].DesiredState != domain.OperationalIntentExternalStateWithdrawn {
		t.Fatalf("replacement after lease = %#v, %v", claimed, err)
	}
}

func TestAcceptedRequestClearsWithdrawnReferenceAcrossReplicas(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 10, 13, 0, 0, 0, time.UTC)
	intent := integrationIntent("77777777-7777-4777-8777-777777777777", now)
	intent.Status = domain.IntentStatusAccepted
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	accepted := domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version,
		DesiredState: domain.OperationalIntentExternalStateAccepted, NextAttemptAt: now, UpdatedAt: now,
	}
	if err := first.RequestOperationalIntentPublication(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	publication, err := first.ClaimOperationalIntentPublication(ctx, intent.ID, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	publication.SyncStatus = domain.PublicationSyncWithdrawn
	publication.ConfirmedState = domain.OperationalIntentExternalStateWithdrawn
	publication.DSSVersion = 2
	publication.OVN = "deleted-ovn"
	publication.ReferenceJSON = []byte(`{"id":"deleted"}`)
	publication.ConfirmedAt = &now
	if err := first.ConfirmOperationalIntentPublication(ctx, publication, publication.Revision, nil); err != nil {
		t.Fatal(err)
	}
	if err := second.RequestOperationalIntentPublication(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stored, err := first.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OVN != "" || stored.DSSVersion != 0 || len(stored.ReferenceJSON) != 0 || stored.ConfirmedAt != nil {
		t.Fatalf("republish retained deleted reference: %#v", stored)
	}
}

func TestConcurrentFindingReplacementsDoNotMerge(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("finding-intent", now)
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	sets := [][]domain.ConflictFinding{
		{{ID: "first-a", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}, {ID: "first-b", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}},
		{{ID: "second-a", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}, {ID: "second-b", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}},
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for index, store := range []*Store{first, second} {
		index, store := index, store
		go func() {
			<-start
			errs <- store.ReplaceConflictFindings(ctx, intent.ID, 1, "rule", sets[index])
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	findings, err := first.ListConflictFindings(ctx, intent.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %#v, want exactly one two-finding replacement set", findings)
	}
	if findings[0].ID[:5] != findings[1].ID[:5] {
		t.Fatalf("replacement sets merged: %#v", findings)
	}
}

func TestPostgresIntegrityAndReplacementScope(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("integrity-intent", now)
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, intent); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want ErrAlreadyExists", err)
	}
	original := integrationVolume("original", intent.ID, 1, now, "")
	if err := store.RecordOperationalVolume(ctx, original); err != nil {
		t.Fatal(err)
	}
	wrong := original
	wrong.ID = "wrong"
	wrong.IntentID = "another-intent"
	if err := store.ReplaceOperationalVolumes(ctx, intent.ID, 1, []domain.OperationalVolume{wrong}); err == nil {
		t.Fatal("expected volume replacement scope error")
	}
	volumes, err := store.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 || volumes[0].ID != original.ID {
		t.Fatalf("volumes after rejected replacement = %#v", volumes)
	}
	orphan := domain.ConflictFinding{ID: "orphan", IntentID: "missing", IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}
	if err := store.RecordConflictFinding(ctx, orphan); err == nil {
		t.Fatal("expected foreign key error for orphan finding")
	}
}

func TestOperationalIntentListsPersistAcrossStoreInstances(t *testing.T) {
	ctx, writer, reader := integrationStores(t)
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)

	primaryV1 := integrationIntent("list-primary", now.Add(2*time.Hour))
	primaryV1.AircraftID = "aircraft-list-one"
	primaryV1.Name = "primary-v1"
	if err := writer.CreateOperationalIntent(ctx, primaryV1); err != nil {
		t.Fatal(err)
	}
	primaryV2 := primaryV1
	primaryV2.Version = 2
	primaryV2.Name = "primary-v2"
	primaryV2.UpdatedAt = now.Add(time.Minute)
	if err := writer.ReplaceOperationalIntent(ctx, primaryV1.Version, primaryV1.Revision, primaryV2, nil); err != nil {
		t.Fatal(err)
	}

	earlier := integrationIntent("list-earlier", now)
	earlier.AircraftID = primaryV1.AircraftID
	if err := writer.CreateOperationalIntent(ctx, earlier); err != nil {
		t.Fatal(err)
	}
	other := integrationIntent("list-other-aircraft", now.Add(time.Hour))
	other.AircraftID = "aircraft-list-two"
	if err := writer.CreateOperationalIntent(ctx, other); err != nil {
		t.Fatal(err)
	}

	versions, err := reader.ListOperationalIntentVersions(ctx, primaryV1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || versions[0].Name != "primary-v1" || versions[1].Version != 2 || versions[1].Name != "primary-v2" {
		t.Fatalf("persisted versions = %#v", versions)
	}
	filtered, err := reader.ListOperationalIntents(ctx, primaryV1.AircraftID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || filtered[0].ID != earlier.ID || filtered[1].ID != primaryV2.ID || filtered[1].Version != 2 {
		t.Fatalf("aircraft-filtered current intents = %#v", filtered)
	}
	all, err := reader.ListOperationalIntents(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all current intents = %#v, want three latest records", all)
	}
}

func integrationStores(t *testing.T) (context.Context, *Store, *Store) {
	t.Helper()
	ctx := context.Background()
	first, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	second, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if _, err := first.pool.Exec(ctx, `TRUNCATE mission_deployments, mission_items, missions, flight_records, aircraft, received_peer_notifications, peer_notifications, operational_intent_publications, conflict_findings, operational_volumes, operational_intents`); err != nil {
		t.Fatal(err)
	}
	return ctx, first, second
}

func integrationIntent(id string, now time.Time) domain.OperationalIntent {
	return domain.OperationalIntent{
		ID: id, Version: 1, AircraftID: id + "-aircraft", Status: domain.IntentStatusDraft,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
}

func integrationVolume(id, intentID string, version int, now time.Time, geoJSON string) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID: id, IntentID: intentID, IntentVersion: version,
		MinAltitudeM: 20, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour), GeoJSON: geoJSON,
	}
}
