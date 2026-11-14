package booking

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("reservation not found")
	ErrHallNotFound     = errors.New("hall not found")
	ErrTableUnavailable = errors.New("table is unavailable")
	ErrKeyReused        = errors.New("idempotency key used with different parameters")
	ErrStaleReservation = errors.New("reservation version or status changed")
	ErrInvalidVersion   = errors.New("version must be positive")
	ErrInvalidKey       = errors.New("invalid idempotency key")
)

func ValidKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

type Snapshot struct {
	ID         int64
	TableID    int64
	GuestCount int
	StartsAt   time.Time
	EndsAt     time.Time
	Status     string
	Version    int64
}

type MoveRequest struct {
	ReservationID   int64
	TableID         int64
	StartsAt        time.Time
	ExpectedVersion int64
}

type FloorPlan struct {
	ID       int64
	Name     string
	TimeZone string
	Width    int
	Height   int
	Tables   []FloorTable
}

type FloorTable struct {
	ID       int64
	Label    string
	Capacity int
	Shape    string
	X        int
	Y        int
	Size     int
}

type Store interface {
	GetForOwner(ctx context.Context, userID, id int64) (Snapshot, error)
	Create(ctx context.Context, userID int64, key string, request Request) (Snapshot, error)
	Move(ctx context.Context, request MoveRequest) (Snapshot, error)
	FloorPlan(ctx context.Context, hallID int64) (FloorPlan, error)
}
