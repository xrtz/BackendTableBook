package booking

import (
	"errors"
	"time"
)

const ReservationDuration = 2 * time.Hour

var (
	ErrInvalidUser  = errors.New("invalid user ID")
	ErrInvalidTable = errors.New("invalid table ID or capacity")
	ErrInvalidParty = errors.New("guest count must be positive and fit the table")
	ErrInvalidStart = errors.New("start must be current or future and the interval must fit RFC 3339")
)

type Table struct {
	ID       int64
	Capacity int
}

type Request struct {
	TableID    int64
	GuestCount int
	StartsAt   time.Time
}

type Reservation struct {
	userID     int64
	tableID    int64
	guestCount int
	startsAt   time.Time
	endsAt     time.Time
}

func NewReservation(userID int64, table Table, request Request, now time.Time) (Reservation, error) {
	if userID <= 0 {
		return Reservation{}, ErrInvalidUser
	}
	if table.ID <= 0 || table.ID != request.TableID || table.Capacity <= 0 {
		return Reservation{}, ErrInvalidTable
	}
	if request.GuestCount <= 0 || request.GuestCount > table.Capacity {
		return Reservation{}, ErrInvalidParty
	}
	start := request.StartsAt.UTC()
	if start.IsZero() || start.Before(now) {
		return Reservation{}, ErrInvalidStart
	}
	end := start.Add(ReservationDuration)
	if start.Year() < 1 || end.Year() > 9999 {
		return Reservation{}, ErrInvalidStart
	}
	return Reservation{
		userID:     userID,
		tableID:    table.ID,
		guestCount: request.GuestCount,
		startsAt:   start,
		endsAt:     end,
	}, nil
}

func (r Reservation) UserID() int64       { return r.userID }
func (r Reservation) TableID() int64      { return r.tableID }
func (r Reservation) GuestCount() int     { return r.guestCount }
func (r Reservation) StartsAt() time.Time { return r.startsAt }
func (r Reservation) EndsAt() time.Time   { return r.endsAt }

// ConflictsWith reports whether both reservations occupy the same table during overlapping intervals.
func (r Reservation) ConflictsWith(other Reservation) bool {
	return r.tableID == other.tableID && r.startsAt.Before(other.endsAt) && other.startsAt.Before(r.endsAt)
}
