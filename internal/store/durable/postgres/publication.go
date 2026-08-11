package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/jackc/pgx/v5"
)

func (s *Store) AcceptOperationalIntentAndRequestPublication(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin intent acceptance and publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	if err := acceptOperationalIntentTx(ctx, tx, intent, expectedRevision); err != nil {
		return err
	}
	if err := requestPublicationTx(ctx, tx, publication); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit intent acceptance and publication: %w", err)
	}
	return nil
}

func (s *Store) UpdateOperationalIntentAndRequestPublication(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin intent update and publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	if err := updateOperationalIntentTx(ctx, tx, intent, expectedRevision); err != nil {
		return err
	}
	if err := requestPublicationTx(ctx, tx, publication); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit intent update and publication: %w", err)
	}
	return nil
}

func (s *Store) RequestOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publication request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, publication.IntentID); err != nil {
		return err
	}
	if err := requestPublicationTx(ctx, tx, publication); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication request: %w", err)
	}
	return nil
}

func requestPublicationTx(ctx context.Context, tx pgx.Tx, request domain.OperationalIntentPublication) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operational_intents WHERE id = $1 AND version = $2)`, request.IntentID, request.DesiredIntentVersion).Scan(&exists); err != nil {
		return fmt.Errorf("check publication intent version: %w", err)
	}
	if !exists {
		return durable.ErrNotFound
	}
	request.LeaseUntil = nil
	current, err := scanPublication(tx.QueryRow(ctx, `SELECT data, revision FROM operational_intent_publications WHERE intent_id = $1 FOR UPDATE`, request.IntentID))
	if err != nil && !errors.Is(err, durable.ErrNotFound) {
		return err
	}
	if err == nil {
		request.Revision = current.Revision + 1
		request.PublishedIntentVersion = current.PublishedIntentVersion
		request.ConfirmedState = current.ConfirmedState
		request.DSSVersion = current.DSSVersion
		request.OVN = current.OVN
		request.SubscriptionID = current.SubscriptionID
		request.Manager = current.Manager
		request.USSBaseURL = current.USSBaseURL
		request.ReferenceJSON = append([]byte(nil), current.ReferenceJSON...)
		request.ConfirmedAt = current.ConfirmedAt
		request.LeaseUntil = current.LeaseUntil
		request.LastAttemptAt = current.LastAttemptAt
	}
	request.SyncStatus = domain.PublicationSyncPending
	request.AttemptCount = 0
	request.LastError = ""
	return writePublication(ctx, tx, request)
}

func (s *Store) GetOperationalIntentPublication(ctx context.Context, intentID string) (domain.OperationalIntentPublication, error) {
	return scanPublication(s.pool.QueryRow(ctx, `SELECT data, revision FROM operational_intent_publications WHERE intent_id = $1`, intentID))
}

func (s *Store) ClaimOperationalIntentPublication(ctx context.Context, intentID string, now, leaseUntil time.Time) (domain.OperationalIntentPublication, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OperationalIntentPublication{}, fmt.Errorf("begin publication claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	publication, err := scanPublication(tx.QueryRow(ctx, `SELECT data, revision FROM operational_intent_publications WHERE intent_id = $1 FOR UPDATE`, intentID))
	if err != nil {
		return domain.OperationalIntentPublication{}, err
	}
	if publication.LeaseUntil != nil && publication.LeaseUntil.After(now) {
		return domain.OperationalIntentPublication{}, durable.ErrVersionConflict
	}
	claimPublication(&publication, now, leaseUntil)
	if err := writePublication(ctx, tx, publication); err != nil {
		return domain.OperationalIntentPublication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OperationalIntentPublication{}, fmt.Errorf("commit publication claim: %w", err)
	}
	return publication, nil
}

func (s *Store) ClaimDueOperationalIntentPublications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]domain.OperationalIntentPublication, error) {
	if limit <= 0 {
		return []domain.OperationalIntentPublication{}, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin due publication claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT data, revision FROM operational_intent_publications
		WHERE sync_status IN ('pending', 'processing', 'retrying')
		  AND next_attempt_at <= $1
		  AND (lease_until IS NULL OR lease_until <= $1)
		ORDER BY next_attempt_at, intent_id
		FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due publications: %w", err)
	}
	publications, err := readPublications(rows)
	if err != nil {
		return nil, err
	}
	for index := range publications {
		claimPublication(&publications[index], now, leaseUntil)
		if err := writePublication(ctx, tx, publications[index]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit due publication claims: %w", err)
	}
	return publications, nil
}

func claimPublication(publication *domain.OperationalIntentPublication, now, leaseUntil time.Time) {
	publication.Revision++
	publication.SyncStatus = domain.PublicationSyncProcessing
	publication.LeaseUntil = &leaseUntil
	publication.LastAttemptAt = &now
	publication.AttemptCount++
	publication.UpdatedAt = now
}

func (s *Store) UpdateOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64) error {
	return updateOperationalIntentPublication(ctx, s.pool, publication, expectedRevision)
}

func updateOperationalIntentPublication(ctx context.Context, db querier, publication domain.OperationalIntentPublication, expectedRevision int64) error {
	publication.Revision = expectedRevision + 1
	publication.LeaseUntil = nil
	raw, err := json.Marshal(publication)
	if err != nil {
		return fmt.Errorf("encode publication: %w", err)
	}
	tag, err := db.Exec(ctx, `
		UPDATE operational_intent_publications SET
			revision = $3, desired_intent_version = $4,
			published_intent_version = NULLIF($5, 0), desired_state = $6,
			sync_status = $7, next_attempt_at = $8, lease_until = NULL,
			updated_at = $9, data = $10
		WHERE intent_id = $1 AND revision = $2`, publication.IntentID, expectedRevision,
		publication.Revision, publication.DesiredIntentVersion, publication.PublishedIntentVersion,
		publication.DesiredState, publication.SyncStatus, publication.NextAttemptAt,
		publication.UpdatedAt, raw)
	if err != nil {
		return fmt.Errorf("update publication: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return durable.ErrVersionConflict
	}
	return nil
}

func (s *Store) ConfirmOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64, notifications []domain.PeerNotification) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publication confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := updateOperationalIntentPublication(ctx, tx, publication, expectedRevision); err != nil {
		return err
	}
	if err := enqueuePeerNotifications(ctx, tx, notifications); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication confirmation: %w", err)
	}
	return nil
}

func enqueuePeerNotifications(ctx context.Context, db querier, notifications []domain.PeerNotification) error {
	for _, notification := range notifications {
		raw, err := json.Marshal(notification)
		if err != nil {
			return fmt.Errorf("encode peer notification: %w", err)
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO peer_notifications (id, revision, intent_id, intent_version, uss_base_url, next_attempt_at, delivered_at, lease_until, updated_at, data)
			VALUES ($1,0,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (id) DO NOTHING`, notification.ID, notification.IntentID,
			notification.IntentVersion, notification.USSBaseURL, notification.NextAttemptAt,
			notification.DeliveredAt, notification.LeaseUntil, notification.UpdatedAt, raw); err != nil {
			return fmt.Errorf("enqueue peer notification: %w", err)
		}
	}
	return nil
}

func (s *Store) ClaimDuePeerNotifications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]domain.PeerNotification, error) {
	if limit <= 0 {
		return []domain.PeerNotification{}, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin peer notification claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT data, revision FROM peer_notifications
		WHERE delivered_at IS NULL AND next_attempt_at <= $1
		  AND (lease_until IS NULL OR lease_until <= $1)
		ORDER BY next_attempt_at, id FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due peer notifications: %w", err)
	}
	notifications, err := readPeerNotifications(rows)
	if err != nil {
		return nil, err
	}
	for index := range notifications {
		notifications[index].Revision++
		notifications[index].LeaseUntil = &leaseUntil
		notifications[index].AttemptCount++
		notifications[index].UpdatedAt = now
		if err := writePeerNotification(ctx, tx, notifications[index]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit peer notification claim: %w", err)
	}
	return notifications, nil
}

func (s *Store) UpdatePeerNotification(ctx context.Context, notification domain.PeerNotification, expectedRevision int64) error {
	notification.Revision = expectedRevision + 1
	notification.LeaseUntil = nil
	raw, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode peer notification: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE peer_notifications SET revision=$3, next_attempt_at=$4,
			delivered_at=$5, lease_until=NULL, updated_at=$6, data=$7
		WHERE id=$1 AND revision=$2`, notification.ID, expectedRevision, notification.Revision,
		notification.NextAttemptAt, notification.DeliveredAt, notification.UpdatedAt, raw)
	if err != nil {
		return fmt.Errorf("update peer notification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return durable.ErrVersionConflict
	}
	return nil
}

func writePeerNotification(ctx context.Context, db querier, notification domain.PeerNotification) error {
	raw, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode peer notification: %w", err)
	}
	_, err = db.Exec(ctx, `UPDATE peer_notifications SET revision=$2, next_attempt_at=$3,
		delivered_at=$4, lease_until=$5, updated_at=$6, data=$7 WHERE id=$1`,
		notification.ID, notification.Revision, notification.NextAttemptAt,
		notification.DeliveredAt, notification.LeaseUntil, notification.UpdatedAt, raw)
	if err != nil {
		return fmt.Errorf("write peer notification: %w", err)
	}
	return nil
}

func readPeerNotifications(rows pgx.Rows) ([]domain.PeerNotification, error) {
	defer rows.Close()
	notifications := make([]domain.PeerNotification, 0)
	for rows.Next() {
		var raw []byte
		var revision int64
		if err := rows.Scan(&raw, &revision); err != nil {
			return nil, fmt.Errorf("scan peer notification: %w", err)
		}
		var notification domain.PeerNotification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return nil, fmt.Errorf("decode peer notification: %w", err)
		}
		notification.Revision = revision
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate peer notifications: %w", err)
	}
	return notifications, nil
}

func (s *Store) RecordReceivedPeerNotification(ctx context.Context, notification domain.ReceivedPeerNotification) error {
	raw, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode received peer notification: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO received_peer_notifications (id, intent_id, manager, intent_version, received_at, data)
		VALUES ($1,$2,$3,NULLIF($4,0),$5,$6)
		ON CONFLICT (id) DO NOTHING`, notification.ID, notification.IntentID,
		notification.Manager, notification.IntentVersion, notification.ReceivedAt, raw); err != nil {
		return fmt.Errorf("record received peer notification: %w", err)
	}
	return nil
}

func (s *Store) ListReceivedPeerNotifications(ctx context.Context, intentID string) ([]domain.ReceivedPeerNotification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT data FROM received_peer_notifications
		WHERE $1 = '' OR intent_id = $1
		ORDER BY received_at, id`, intentID)
	if err != nil {
		return nil, fmt.Errorf("list received peer notifications: %w", err)
	}
	defer rows.Close()
	notifications := make([]domain.ReceivedPeerNotification, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan received peer notification: %w", err)
		}
		var notification domain.ReceivedPeerNotification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return nil, fmt.Errorf("decode received peer notification: %w", err)
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate received peer notifications: %w", err)
	}
	return notifications, nil
}

func writePublication(ctx context.Context, db querier, publication domain.OperationalIntentPublication) error {
	raw, err := json.Marshal(publication)
	if err != nil {
		return fmt.Errorf("encode publication: %w", err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO operational_intent_publications (
			intent_id, revision, desired_intent_version, published_intent_version,
			desired_state, sync_status, next_attempt_at, lease_until, updated_at, data
		) VALUES ($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8,$9,$10)
		ON CONFLICT (intent_id) DO UPDATE SET
			revision=EXCLUDED.revision,
			desired_intent_version=EXCLUDED.desired_intent_version,
			published_intent_version=EXCLUDED.published_intent_version,
			desired_state=EXCLUDED.desired_state,
			sync_status=EXCLUDED.sync_status,
			next_attempt_at=EXCLUDED.next_attempt_at,
			lease_until=EXCLUDED.lease_until,
			updated_at=EXCLUDED.updated_at,
			data=EXCLUDED.data`, publication.IntentID, publication.Revision,
		publication.DesiredIntentVersion, publication.PublishedIntentVersion,
		publication.DesiredState, publication.SyncStatus, publication.NextAttemptAt,
		publication.LeaseUntil, publication.UpdatedAt, raw)
	if err != nil {
		return fmt.Errorf("write publication: %w", err)
	}
	return nil
}

func scanPublication(row pgx.Row) (domain.OperationalIntentPublication, error) {
	var raw []byte
	var revision int64
	if err := row.Scan(&raw, &revision); errors.Is(err, pgx.ErrNoRows) {
		return domain.OperationalIntentPublication{}, durable.ErrNotFound
	} else if err != nil {
		return domain.OperationalIntentPublication{}, fmt.Errorf("read publication: %w", err)
	}
	var publication domain.OperationalIntentPublication
	if err := json.Unmarshal(raw, &publication); err != nil {
		return domain.OperationalIntentPublication{}, fmt.Errorf("decode publication: %w", err)
	}
	publication.Revision = revision
	return publication, nil
}

func readPublications(rows pgx.Rows) ([]domain.OperationalIntentPublication, error) {
	defer rows.Close()
	publications := make([]domain.OperationalIntentPublication, 0)
	for rows.Next() {
		publication, err := scanPublication(rows)
		if err != nil {
			return nil, err
		}
		publications = append(publications, publication)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate publications: %w", err)
	}
	return publications, nil
}
