package booking

import (
	"errors"
	"time"
)

var ErrGuestNotFound = errors.New("guest account not found")

type AdminFilter struct {
	HallID int64
	From   time.Time
	To     time.Time
	Status string
}

type AdminReservation struct {
	Snapshot
	TableLabel string
	GuestEmail string
}

func CanTransition(from, to string) bool {
	switch from {
	case "confirmed":
		return to == "seated" || to == "cancelled"
	case "seated":
		return to == "completed"
	default:
		return false
	}
}
