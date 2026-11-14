package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"tablebook/internal/booking"
)

func (s *Store) Halls(ctx context.Context) ([]booking.Hall, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, name, time_zone FROM halls ORDER BY id")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[booking.Hall])
}

func (s *Store) Availability(ctx context.Context, hallID int64, query booking.AvailabilityQuery) ([]booking.TableAvailability, error) {
	start := query.StartsAt.UTC()
	if !validTimestamp(start) || start.Before(s.now()) {
		return nil, booking.ErrInvalidStart
	}
	if query.GuestCount <= 0 {
		return nil, booking.ErrInvalidParty
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM halls WHERE id=$1)", hallID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, booking.ErrHallNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.capacity >= $4 AND NOT EXISTS (
			SELECT 1 FROM reservations r
			WHERE r.table_id=t.id AND r.status IN ('confirmed','seated','completed')
			  AND r.occupied_at && tstzrange($2, $3, '[)')
		)
		FROM dining_tables t WHERE t.hall_id=$1 ORDER BY t.id`,
		hallID, start, start.Add(booking.ReservationDuration), int64(query.GuestCount))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[booking.TableAvailability])
}

func (s *Store) ListForOwner(ctx context.Context, userID int64) ([]booking.OwnedReservation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.table_id, r.guest_count, r.starts_at, r.ends_at, r.status, r.version,
			t.label, h.name, h.time_zone
		FROM reservations r JOIN dining_tables t ON t.id=r.table_id JOIN halls h ON h.id=t.hall_id
		WHERE r.user_id=$1
		ORDER BY r.starts_at DESC, r.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (booking.OwnedReservation, error) {
		var r booking.OwnedReservation
		err := row.Scan(&r.ID, &r.TableID, &r.GuestCount, &r.StartsAt, &r.EndsAt, &r.Status, &r.Version,
			&r.TableLabel, &r.HallName, &r.TimeZone)
		r.StartsAt, r.EndsAt = r.StartsAt.UTC(), r.EndsAt.UTC()
		return r, err
	})
}

func (s *Store) CancelForOwner(ctx context.Context, userID, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	reservation, err := scanSnapshot(tx.QueryRow(ctx, "SELECT "+snapshotFields+
		" FROM reservations WHERE id=$1 AND user_id=$2 FOR UPDATE", id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return booking.ErrNotFound
	}
	if err != nil {
		return err
	}
	if reservation.Status == "cancelled" {
		return nil
	}
	if reservation.Status != "confirmed" || !reservation.StartsAt.After(s.now()) {
		return booking.ErrCannotCancel
	}
	if _, err := tx.Exec(ctx, "UPDATE reservations SET status='cancelled', version=version+1 WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
