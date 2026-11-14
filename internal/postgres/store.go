package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tablebook/internal/booking"
)

type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func New(pool *pgxpool.Pool, now func() time.Time) *Store {
	return &Store{pool: pool, now: now}
}

const snapshotFields = "id, table_id, guest_count, starts_at, ends_at, status, version"

func (s *Store) GetForOwner(ctx context.Context, userID, id int64) (booking.Snapshot, error) {
	result, err := scanSnapshot(s.pool.QueryRow(ctx,
		"SELECT "+snapshotFields+" FROM reservations WHERE id = $1 AND user_id = $2", id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = booking.ErrNotFound
	}
	return result, err
}

func (s *Store) Create(ctx context.Context, userID int64, key string, request booking.Request) (booking.Snapshot, error) {
	return retryTransaction(ctx, func() (booking.Snapshot, error) {
		return s.create(ctx, userID, key, request)
	})
}

func (s *Store) create(ctx context.Context, userID int64, key string, request booking.Request) (booking.Snapshot, error) {
	if userID <= 0 {
		return booking.Snapshot{}, booking.ErrInvalidUser
	}
	if !booking.ValidKey(key) {
		return booking.Snapshot{}, booking.ErrInvalidKey
	}
	if request.TableID <= 0 {
		return booking.Snapshot{}, booking.ErrInvalidTable
	}
	if request.GuestCount <= 0 || request.GuestCount > math.MaxInt32 {
		return booking.Snapshot{}, booking.ErrInvalidParty
	}
	start := request.StartsAt.UTC()
	if !validTimestamp(start) {
		return booking.Snapshot{}, booking.ErrInvalidStart
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return booking.Snapshot{}, err
	}
	defer rollback(tx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO reservation_create_keys (user_id, key, table_id, guest_count, starts_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, key) DO NOTHING`,
		userID, key, request.TableID, request.GuestCount, start)
	if err != nil {
		return booking.Snapshot{}, err
	}
	if tag.RowsAffected() == 0 {
		// This statement gets a fresh snapshot after the conflicting insert commits.
		return readCreateResponse(ctx, tx, userID, key, request)
	}

	table, err := loadTable(ctx, tx, request.TableID)
	if err != nil {
		return booking.Snapshot{}, err
	}
	reservation, err := booking.NewReservation(userID, table, request, s.now())
	if err != nil {
		return booking.Snapshot{}, err
	}
	result, err := scanSnapshot(tx.QueryRow(ctx, `
		INSERT INTO reservations (user_id, table_id, guest_count, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+snapshotFields,
		reservation.UserID(), reservation.TableID(), reservation.GuestCount(), reservation.StartsAt(), reservation.EndsAt()))
	if err != nil {
		return booking.Snapshot{}, translateWriteError(err)
	}
	response, err := json.Marshal(result)
	if err != nil {
		return booking.Snapshot{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE reservation_create_keys SET response = $3 WHERE user_id = $1 AND key = $2", userID, key, response); err != nil {
		return booking.Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return booking.Snapshot{}, err
	}
	return result, nil
}

func readCreateResponse(ctx context.Context, tx pgx.Tx, userID int64, key string, request booking.Request) (booking.Snapshot, error) {
	var matches bool
	var response []byte
	err := tx.QueryRow(ctx, `
		SELECT (table_id = $3 AND guest_count = $4 AND starts_at = $5), response
		FROM reservation_create_keys WHERE user_id = $1 AND key = $2`,
		userID, key, request.TableID, request.GuestCount, request.StartsAt).Scan(&matches, &response)
	if err != nil {
		return booking.Snapshot{}, err
	}
	if !matches {
		return booking.Snapshot{}, booking.ErrKeyReused
	}
	var result booking.Snapshot
	if err := json.Unmarshal(response, &result); err != nil {
		return booking.Snapshot{}, fmt.Errorf("decode recorded reservation: %w", err)
	}
	return result, nil
}

func (s *Store) Move(ctx context.Context, request booking.MoveRequest) (booking.Snapshot, error) {
	return retryTransaction(ctx, func() (booking.Snapshot, error) {
		return s.move(ctx, request)
	})
}

func (s *Store) move(ctx context.Context, request booking.MoveRequest) (booking.Snapshot, error) {
	if request.ReservationID <= 0 {
		return booking.Snapshot{}, booking.ErrNotFound
	}
	if request.ExpectedVersion <= 0 {
		return booking.Snapshot{}, booking.ErrInvalidVersion
	}
	if !validTimestamp(request.StartsAt.UTC()) {
		return booking.Snapshot{}, booking.ErrInvalidStart
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return booking.Snapshot{}, err
	}
	defer rollback(tx)

	var userID, version int64
	var guestCount int
	var status string
	err = tx.QueryRow(ctx, `
		SELECT user_id, guest_count, status, version FROM reservations WHERE id = $1 FOR UPDATE`,
		request.ReservationID).Scan(&userID, &guestCount, &status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return booking.Snapshot{}, booking.ErrNotFound
	}
	if err != nil {
		return booking.Snapshot{}, err
	}
	if status != "confirmed" || version != request.ExpectedVersion {
		return booking.Snapshot{}, booking.ErrStaleReservation
	}
	table, err := loadTable(ctx, tx, request.TableID)
	if err != nil {
		return booking.Snapshot{}, err
	}
	reservation, err := booking.NewReservation(userID, table, booking.Request{
		TableID: request.TableID, GuestCount: guestCount, StartsAt: request.StartsAt,
	}, s.now())
	if err != nil {
		return booking.Snapshot{}, err
	}
	result, err := scanSnapshot(tx.QueryRow(ctx, `
		UPDATE reservations SET table_id = $2, starts_at = $3, ends_at = $4, version = version + 1
		WHERE id = $1 RETURNING `+snapshotFields,
		request.ReservationID, reservation.TableID(), reservation.StartsAt(), reservation.EndsAt()))
	if err != nil {
		return booking.Snapshot{}, translateWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return booking.Snapshot{}, err
	}
	return result, nil
}

func (s *Store) FloorPlan(ctx context.Context, hallID int64) (booking.FloorPlan, error) {
	var plan booking.FloorPlan
	err := s.pool.QueryRow(ctx, "SELECT id, name, time_zone, width, height FROM halls WHERE id = $1", hallID).
		Scan(&plan.ID, &plan.Name, &plan.TimeZone, &plan.Width, &plan.Height)
	if errors.Is(err, pgx.ErrNoRows) {
		return plan, booking.ErrHallNotFound
	}
	if err != nil {
		return plan, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, label, capacity, shape, x, y, size FROM dining_tables WHERE hall_id = $1 ORDER BY id`, hallID)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	plan.Tables = make([]booking.FloorTable, 0)
	for rows.Next() {
		var table booking.FloorTable
		if err := rows.Scan(&table.ID, &table.Label, &table.Capacity, &table.Shape, &table.X, &table.Y, &table.Size); err != nil {
			return booking.FloorPlan{}, err
		}
		plan.Tables = append(plan.Tables, table)
	}
	return plan, rows.Err()
}

func loadTable(ctx context.Context, tx pgx.Tx, tableID int64) (booking.Table, error) {
	var table booking.Table
	err := tx.QueryRow(ctx, "SELECT id, capacity FROM dining_tables WHERE id = $1 FOR NO KEY UPDATE", tableID).
		Scan(&table.ID, &table.Capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return table, booking.ErrInvalidTable
	}
	return table, err
}

func scanSnapshot(row pgx.Row) (booking.Snapshot, error) {
	var result booking.Snapshot
	err := row.Scan(&result.ID, &result.TableID, &result.GuestCount, &result.StartsAt, &result.EndsAt, &result.Status, &result.Version)
	result.StartsAt = result.StartsAt.UTC()
	result.EndsAt = result.EndsAt.UTC()
	return result, err
}

func validTimestamp(start time.Time) bool {
	return !start.IsZero() && start.Year() >= 1 && start.Add(booking.ReservationDuration).Year() <= 9999 &&
		start.Nanosecond()%1000 == 0
}

func translateWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23P01" &&
		postgresError.ConstraintName == "reservations_table_interval_excl" {
		return booking.ErrTableUnavailable
	}
	return err
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func retryTransaction(ctx context.Context, run func() (booking.Snapshot, error)) (booking.Snapshot, error) {
	for attempt := 1; ; attempt++ {
		result, err := run()
		var pgErr *pgconn.PgError
		if attempt == 3 || ctx.Err() != nil || !errors.As(err, &pgErr) ||
			(pgErr.Code != "40P01" && pgErr.Code != "40001") {
			return result, err
		}
	}
}
