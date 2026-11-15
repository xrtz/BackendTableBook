package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"tablebook/internal/booking"
)

func (s *Store) AdminReservations(ctx context.Context, filter booking.AdminFilter) ([]booking.AdminReservation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.table_id, r.guest_count, r.starts_at, r.ends_at, r.status, r.version,
			t.label, COALESCE(a.email, '')
		FROM reservations r
		JOIN dining_tables t ON t.id=r.table_id
		LEFT JOIN accounts a ON a.id=r.user_id
		WHERE t.hall_id=$1 AND r.starts_at < $3 AND r.ends_at > $2
		  AND ($4 = '' OR r.status=$4)
		ORDER BY r.starts_at, r.id`, filter.HallID, filter.From, filter.To, filter.Status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (booking.AdminReservation, error) {
		var r booking.AdminReservation
		err := row.Scan(&r.ID, &r.TableID, &r.GuestCount, &r.StartsAt, &r.EndsAt, &r.Status, &r.Version,
			&r.TableLabel, &r.GuestEmail)
		return r, err
	})
}

func (s *Store) CreateForGuest(ctx context.Context, email, key string, request booking.Request) (booking.Snapshot, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM accounts WHERE email=$1", strings.ToLower(strings.TrimSpace(email))).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return booking.Snapshot{}, booking.ErrGuestNotFound
	}
	if err != nil {
		return booking.Snapshot{}, err
	}
	return s.Create(ctx, id, key, request)
}

func (s *Store) ChangeStatus(ctx context.Context, id, version int64, target string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var current string
	var actualVersion int64
	err = tx.QueryRow(ctx, "SELECT status, version FROM reservations WHERE id=$1 FOR UPDATE", id).
		Scan(&current, &actualVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return booking.ErrNotFound
	}
	if err != nil {
		return err
	}
	if version != actualVersion || !booking.CanTransition(current, target) {
		return booking.ErrStaleReservation
	}
	if _, err := tx.Exec(ctx, "UPDATE reservations SET status=$2, version=version+1 WHERE id=$1", id, target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
