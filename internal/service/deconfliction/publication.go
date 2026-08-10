package deconfliction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	dss "github.com/Aero-Arc/dss-clients/interuss"
)

const (
	publicationLease = 30 * time.Second
	publicationBatch = 20
)

func (s *DeconflictionService) PublishingEnabled() bool {
	return s.publisher != nil && s.publisher.PublicationEnabled() && s.coordination != nil
}

func (s *DeconflictionService) PublicationRequest(intent domain.OperationalIntent, state domain.OperationalIntentExternalState) domain.OperationalIntentPublication {
	now := s.now().UTC()
	return domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version, DesiredState: state,
		SyncStatus: domain.PublicationSyncPending, NextAttemptAt: now, UpdatedAt: now,
	}
}

func (s *DeconflictionService) GetPublication(ctx context.Context, intentID string) (domain.OperationalIntentPublication, error) {
	if s.coordination == nil {
		return domain.OperationalIntentPublication{}, durable.ErrNotFound
	}
	return s.coordination.GetOperationalIntentPublication(ctx, intentID)
}

func (s *DeconflictionService) RecordReceivedPeerNotification(ctx context.Context, notification domain.ReceivedPeerNotification) error {
	if s.coordination == nil {
		return fmt.Errorf("coordination storage is not configured")
	}
	return s.coordination.RecordReceivedPeerNotification(ctx, notification)
}

func (s *DeconflictionService) GetPublishedOperationalIntent(ctx context.Context, intentID string, dssVersion int) (domain.OperationalIntentPublication, []domain.OperationalVolume, error) {
	publication, err := s.GetPublication(ctx, intentID)
	if err != nil {
		return domain.OperationalIntentPublication{}, nil, err
	}
	if publication.PublishedIntentVersion == 0 || publication.OVN == "" ||
		publication.SyncStatus == domain.PublicationSyncWithdrawn ||
		dssVersion > 0 && publication.DSSVersion != dssVersion {
		return domain.OperationalIntentPublication{}, nil, durable.ErrNotFound
	}
	volumes, err := s.durable.ListOperationalVolumes(ctx, intentID)
	if err != nil {
		return domain.OperationalIntentPublication{}, nil, err
	}
	return publication, volumesForVersion(volumes, publication.PublishedIntentVersion), nil
}

func (s *DeconflictionService) ReconcileIntent(ctx context.Context, intentID string) error {
	if !s.PublishingEnabled() {
		return fmt.Errorf("DSS publication is not configured")
	}
	now := s.now().UTC()
	publication, err := s.coordination.ClaimOperationalIntentPublication(ctx, intentID, now, now.Add(publicationLease))
	if err != nil {
		return err
	}
	return s.reconcileClaimed(ctx, publication)
}

func (s *DeconflictionService) RunPublicationWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.ReconcileDue(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.WarnContext(ctx, "DSS publication reconciliation failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *DeconflictionService) ReconcileDue(ctx context.Context) error {
	if !s.PublishingEnabled() {
		return nil
	}
	now := s.now().UTC()
	publications, err := s.coordination.ClaimDueOperationalIntentPublications(ctx, now, now.Add(publicationLease), publicationBatch)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, publication := range publications {
		if err := s.reconcileClaimed(ctx, publication); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile intent %s: %w", publication.IntentID, err))
		}
	}
	if err := s.DeliverDuePeerNotifications(ctx); err != nil {
		reconcileErrors = append(reconcileErrors, err)
	}
	return errors.Join(reconcileErrors...)
}

func (s *DeconflictionService) reconcileClaimed(ctx context.Context, publication domain.OperationalIntentPublication) error {
	expectedRevision := publication.Revision
	now := s.now().UTC()
	if publication.DesiredState == domain.OperationalIntentExternalStateWithdrawn {
		if publication.OVN == "" {
			publication.SyncStatus = domain.PublicationSyncWithdrawn
			publication.ConfirmedState = domain.OperationalIntentExternalStateWithdrawn
			publication.ConfirmedAt = &now
			publication.UpdatedAt = now
			return s.coordination.UpdateOperationalIntentPublication(ctx, publication, expectedRevision)
		}
		receipt, err := s.publisher.DeleteOperationalIntent(ctx, publication.IntentID, publication.OVN)
		if err != nil {
			var responseErr *dss.SCDResponseError
			if errors.As(err, &responseErr) && responseErr.StatusCode == 404 {
				publication.SyncStatus = domain.PublicationSyncWithdrawn
				publication.ConfirmedState = domain.OperationalIntentExternalStateWithdrawn
				publication.PublishedIntentVersion = 0
				publication.ConfirmedAt = &now
				publication.UpdatedAt = now
				return s.coordination.UpdateOperationalIntentPublication(ctx, publication, expectedRevision)
			}
			return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
		}
		applyReceipt(&publication, receipt)
		publication.SyncStatus = domain.PublicationSyncWithdrawn
		publication.ConfirmedState = domain.OperationalIntentExternalStateWithdrawn
		publication.PublishedIntentVersion = 0
		publication.ConfirmedAt = &now
		publication.UpdatedAt = now
		request := airspaceprovider.PublicationRequest{Intent: domain.OperationalIntent{ID: publication.IntentID, Version: publication.DesiredIntentVersion}}
		notifications, err := s.buildPeerNotifications(request, receipt, true)
		if err != nil {
			return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
		}
		return s.coordination.ConfirmOperationalIntentPublication(ctx, publication, expectedRevision, notifications)
	}

	intent, err := s.durable.GetOperationalIntentVersion(ctx, publication.IntentID, publication.DesiredIntentVersion)
	if err != nil {
		return s.recordPublicationFailure(ctx, publication, expectedRevision, err, true)
	}
	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
	}
	volumes = volumesForVersion(volumes, intent.Version)
	result, records, err := s.check(ctx, intent, volumes)
	if err != nil {
		return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		return s.recordPublicationFailure(ctx, publication, expectedRevision,
			fmt.Errorf("deconfliction posture is %s", result.Posture), true)
	}
	keySet := make(map[string]struct{})
	for _, record := range records {
		if record.Source.OVN != "" && record.Source.ReferenceID != intent.ID {
			keySet[record.Source.OVN] = struct{}{}
		}
	}
	key := make([]string, 0, len(keySet))
	for ovn := range keySet {
		key = append(key, ovn)
	}
	sort.Strings(key)
	request := airspaceprovider.PublicationRequest{
		Intent: intent, Volumes: volumes, State: publication.DesiredState, Key: key,
		OVN: publication.OVN, SubscriptionID: publication.SubscriptionID,
	}
	var receipt airspaceprovider.PublicationReceipt
	if publication.OVN == "" {
		receipt, err = s.publisher.CreateOperationalIntent(ctx, request)
	} else {
		receipt, err = s.publisher.UpdateOperationalIntent(ctx, request)
	}
	if err != nil {
		current, readErr := s.publisher.GetOperationalIntentReference(ctx, publication.IntentID)
		if readErr == nil && current.Version > publication.DSSVersion && current.State == publication.DesiredState {
			// The mutation succeeded but its response was lost. Subscriber URLs
			// are not recoverable from the read response, but the owned DSS state
			// and published details can be reconciled safely.
			receipt = current
		} else {
			if readErr == nil && current.OVN != "" {
				applyReceipt(&publication, current)
			}
			return s.recordPublicationFailure(ctx, publication, expectedRevision, err, permanentPublicationError(err))
		}
	}
	applyReceipt(&publication, receipt)
	publication.PublishedIntentVersion = publication.DesiredIntentVersion
	publication.ConfirmedState = publication.DesiredState
	publication.SyncStatus = domain.PublicationSyncConfirmed
	publication.ConfirmedAt = &now
	publication.LastError = ""
	publication.UpdatedAt = now
	notifications, err := s.buildPeerNotifications(request, receipt, false)
	if err != nil {
		return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
	}
	return s.coordination.ConfirmOperationalIntentPublication(ctx, publication, expectedRevision, notifications)
}

func permanentPublicationError(err error) bool {
	var responseErr *dss.SCDResponseError
	if !errors.As(err, &responseErr) {
		return false
	}
	return responseErr.StatusCode == 400 || responseErr.StatusCode == 401 ||
		responseErr.StatusCode == 403 || responseErr.StatusCode == 413
}

func (s *DeconflictionService) buildPeerNotifications(request airspaceprovider.PublicationRequest, receipt airspaceprovider.PublicationReceipt, deleted bool) ([]domain.PeerNotification, error) {
	now := s.now().UTC()
	notifications := make([]domain.PeerNotification, 0, len(receipt.Subscribers))
	for _, subscriber := range receipt.Subscribers {
		payload, err := s.publisher.BuildPeerNotification(request, receipt, subscriber, deleted)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", request.Intent.ID, receipt.Version, subscriber.USSBaseURL)))
		notifications = append(notifications, domain.PeerNotification{
			ID: hex.EncodeToString(digest[:]), IntentID: request.Intent.ID,
			IntentVersion: request.Intent.Version, USSBaseURL: subscriber.USSBaseURL,
			Payload: payload, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		})
	}
	return notifications, nil
}

func (s *DeconflictionService) DeliverDuePeerNotifications(ctx context.Context) error {
	if !s.PublishingEnabled() {
		return nil
	}
	now := s.now().UTC()
	notifications, err := s.coordination.ClaimDuePeerNotifications(ctx, now, now.Add(publicationLease), publicationBatch)
	if err != nil {
		return err
	}
	var deliveryErrors []error
	for _, notification := range notifications {
		expectedRevision := notification.Revision
		err := s.publisher.DeliverPeerNotification(ctx, notification.USSBaseURL, notification.Payload)
		now = s.now().UTC()
		notification.UpdatedAt = now
		if err == nil {
			notification.DeliveredAt = &now
			notification.LastError = ""
		} else {
			notification.LastError = err.Error()
			notification.NextAttemptAt = now.Add(time.Second << min(notification.AttemptCount, 8))
			deliveryErrors = append(deliveryErrors, err)
		}
		if updateErr := s.coordination.UpdatePeerNotification(ctx, notification, expectedRevision); updateErr != nil {
			deliveryErrors = append(deliveryErrors, updateErr)
		}
	}
	return errors.Join(deliveryErrors...)
}

func applyReceipt(publication *domain.OperationalIntentPublication, receipt airspaceprovider.PublicationReceipt) {
	publication.Manager = receipt.Manager
	publication.DSSVersion = receipt.Version
	publication.OVN = receipt.OVN
	publication.SubscriptionID = receipt.SubscriptionID
	publication.USSBaseURL = receipt.USSBaseURL
	publication.ReferenceJSON = append([]byte(nil), receipt.ReferenceJSON...)
}

func (s *DeconflictionService) recordPublicationFailure(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64, cause error, permanent bool) error {
	now := s.now().UTC()
	publication.LastError = cause.Error()
	publication.UpdatedAt = now
	if permanent {
		publication.SyncStatus = domain.PublicationSyncBlocked
		publication.NextAttemptAt = time.Time{}
	} else {
		publication.SyncStatus = domain.PublicationSyncRetrying
		delay := time.Second << min(publication.AttemptCount, 8)
		publication.NextAttemptAt = now.Add(delay)
	}
	if err := s.coordination.UpdateOperationalIntentPublication(ctx, publication, expectedRevision); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
