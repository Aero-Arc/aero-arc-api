package deconfliction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	dss "github.com/Aero-Arc/dss-clients/interuss"
)

const (
	publicationLease      = 30 * time.Second
	publicationBatch      = 20
	peerNotificationBatch = 20
)

func (s *DeconflictionService) PublishingEnabled() bool {
	return s.publisher != nil && s.publisher.PublicationEnabled()
}

func (s *DeconflictionService) PublicationRequest(intent domain.OperationalIntent, state domain.OperationalIntentExternalState) domain.OperationalIntentPublication {
	now := s.now().UTC()
	return domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version, DesiredState: state,
		SyncStatus: domain.PublicationSyncPending, NextAttemptAt: now, UpdatedAt: now,
	}
}

func (s *DeconflictionService) GetPublication(ctx context.Context, intentID string) (domain.OperationalIntentPublication, error) {
	return s.durable.GetOperationalIntentPublication(ctx, intentID)
}

func (s *DeconflictionService) RecordReceivedPeerNotification(ctx context.Context, notification domain.ReceivedPeerNotification) error {
	return s.durable.RecordReceivedPeerNotification(ctx, notification)
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
	publication, err := s.durable.ClaimOperationalIntentPublication(ctx, intentID, now, now.Add(s.publicationLease))
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
	var reconcileErrors []error
	for range publicationBatch {
		now := s.now().UTC()
		publications, err := s.durable.ClaimDueOperationalIntentPublications(ctx, now, now.Add(s.publicationLease), 1)
		if err != nil {
			reconcileErrors = append(reconcileErrors, err)
			break
		}
		if len(publications) == 0 {
			break
		}
		if err := s.reconcileClaimed(ctx, publications[0]); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile intent %s: %w", publications[0].IntentID, err))
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
			current, err := s.publisher.GetOperationalIntentReference(ctx, publication.IntentID)
			if err != nil {
				var responseErr *dss.SCDResponseError
				if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
					return s.confirmWithdrawal(ctx, publication, expectedRevision, airspaceprovider.PublicationReceipt{})
				}
				return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
			}
			if current.OVN == "" {
				return s.recordPublicationFailure(ctx, publication, expectedRevision,
					fmt.Errorf("DSS reference omitted manager OVN"), false)
			}
			applyReceipt(&publication, current)
		}
		if err := s.renewPublicationLease(ctx, publication); err != nil {
			return err
		}
		receipt, err := s.publisher.DeleteOperationalIntent(ctx, publication.IntentID, publication.OVN)
		if err != nil {
			var responseErr *dss.SCDResponseError
			if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
				recovered, recoverErr := s.recoverWithdrawalReceipt(ctx, publication)
				if recoverErr != nil {
					return s.recordPublicationFailure(ctx, publication, expectedRevision, recoverErr, false)
				}
				return s.confirmWithdrawal(ctx, publication, expectedRevision, recovered)
			}
			if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusConflict {
				current, readErr := s.publisher.GetOperationalIntentReference(ctx, publication.IntentID)
				if readErr != nil {
					return s.recordPublicationFailure(ctx, publication, expectedRevision, errors.Join(err, readErr), false)
				}
				if current.OVN == "" {
					return s.recordPublicationFailure(ctx, publication, expectedRevision,
						fmt.Errorf("DSS reference omitted manager OVN after delete conflict"), false)
				}
				applyReceipt(&publication, current)
			}
			return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
		}
		return s.confirmWithdrawal(ctx, publication, expectedRevision, receipt)
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
			fmt.Errorf("deconfliction posture is %s", result.Posture), !retryableDeconflictionResult(result))
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
	if err := s.renewPublicationLease(ctx, publication); err != nil {
		return err
	}
	if publication.OVN == "" {
		receipt, err = s.publisher.CreateOperationalIntent(ctx, request)
	} else {
		receipt, err = s.publisher.UpdateOperationalIntent(ctx, request)
	}
	if err != nil {
		current, readErr := s.publisher.GetOperationalIntentReference(ctx, publication.IntentID)
		if readErr == nil && current.Version > publication.DSSVersion && current.State == publication.DesiredState {
			// A read recovers the new OVN but not the mutation response's
			// subscribers. Persist the recovered reference and retry as an update
			// so peer notifications are built from a complete mutation receipt.
			applyReceipt(&publication, current)
			return s.recordPublicationFailure(ctx, publication, expectedRevision,
				fmt.Errorf("DSS mutation response was lost before subscribers were recorded: %w", err), false)
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
	return s.durable.ConfirmOperationalIntentPublication(ctx, publication, expectedRevision, notifications)
}

func permanentPublicationError(err error) bool {
	var responseErr *dss.SCDResponseError
	if !errors.As(err, &responseErr) {
		return false
	}
	return responseErr.StatusCode == 400 || responseErr.StatusCode == 401 ||
		responseErr.StatusCode == 403 || responseErr.StatusCode == 413
}

func retryableDeconflictionResult(result domain.DeconflictionResult) bool {
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		return false
	}
	for _, finding := range result.Findings {
		if finding.Status == domain.ConflictFindingStatusIndeterminate &&
			finding.SourceType == domain.ConflictFindingSourceExternal {
			return true
		}
	}
	return false
}

func (s *DeconflictionService) recoverWithdrawalReceipt(ctx context.Context, publication domain.OperationalIntentPublication) (airspaceprovider.PublicationReceipt, error) {
	volumes, err := s.durable.ListOperationalVolumes(ctx, publication.IntentID)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	version := publication.PublishedIntentVersion
	if version == 0 {
		version = publication.DesiredIntentVersion
	}
	subscribers, err := s.publisher.FindSubscribers(ctx, volumesForVersion(volumes, version))
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("recover subscribers after DSS deletion: %w", err)
	}
	return airspaceprovider.PublicationReceipt{
		Manager: publication.Manager, Version: publication.DSSVersion, OVN: publication.OVN,
		SubscriptionID: publication.SubscriptionID, USSBaseURL: publication.USSBaseURL,
		State: publication.ConfirmedState, ReferenceJSON: append([]byte(nil), publication.ReferenceJSON...),
		Subscribers: subscribers,
	}, nil
}

func (s *DeconflictionService) confirmWithdrawal(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64, receipt airspaceprovider.PublicationReceipt) error {
	now := s.now().UTC()
	if receipt.OVN != "" {
		applyReceipt(&publication, receipt)
	}
	request := airspaceprovider.PublicationRequest{Intent: domain.OperationalIntent{ID: publication.IntentID, Version: publication.DesiredIntentVersion}}
	notifications, err := s.buildPeerNotifications(request, receipt, true)
	if err != nil {
		return s.recordPublicationFailure(ctx, publication, expectedRevision, err, false)
	}
	publication.SyncStatus = domain.PublicationSyncWithdrawn
	publication.ConfirmedState = domain.OperationalIntentExternalStateWithdrawn
	publication.PublishedIntentVersion = 0
	publication.ConfirmedAt = &now
	publication.LastError = ""
	publication.UpdatedAt = now
	return s.durable.ConfirmOperationalIntentPublication(ctx, publication, expectedRevision, notifications)
}

func (s *DeconflictionService) buildPeerNotifications(request airspaceprovider.PublicationRequest, receipt airspaceprovider.PublicationReceipt, deleted bool) ([]domain.PeerNotification, error) {
	now := s.now().UTC()
	notifications := make([]domain.PeerNotification, 0, len(receipt.Subscribers))
	for _, subscriber := range receipt.Subscribers {
		payload, err := s.publisher.BuildPeerNotification(request, receipt, subscriber, deleted)
		if err != nil {
			return nil, err
		}
		identity := fmt.Sprintf("%s\x00%d\x00%s", request.Intent.ID, receipt.Version, subscriber.USSBaseURL)
		if deleted {
			identity = fmt.Sprintf("%s\x00withdrawn\x00%d\x00%s", request.Intent.ID, request.Intent.Version, subscriber.USSBaseURL)
		}
		digest := sha256.Sum256([]byte(identity))
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
	var deliveryErrors []error
	for range peerNotificationBatch {
		now := s.now().UTC()
		notifications, err := s.durable.ClaimDuePeerNotifications(ctx, now, now.Add(s.publicationLease), 1)
		if err != nil {
			deliveryErrors = append(deliveryErrors, err)
			break
		}
		if len(notifications) == 0 {
			break
		}
		notification := notifications[0]
		expectedRevision := notification.Revision
		err = s.publisher.DeliverPeerNotification(ctx, notification.USSBaseURL, notification.Payload)
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
		if updateErr := s.durable.UpdatePeerNotification(ctx, notification, expectedRevision); updateErr != nil {
			deliveryErrors = append(deliveryErrors, updateErr)
		}
	}
	return errors.Join(deliveryErrors...)
}

func (s *DeconflictionService) renewPublicationLease(ctx context.Context, publication domain.OperationalIntentPublication) error {
	leaseUntil := s.now().UTC().Add(s.publicationLease)
	return s.durable.RenewOperationalIntentPublicationLease(ctx, publication.IntentID, publication.Revision, leaseUntil)
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
	if err := s.durable.UpdateOperationalIntentPublication(ctx, publication, expectedRevision); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
