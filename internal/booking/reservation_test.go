package booking

import (
	"errors"
	"testing"
	"time"
)

func TestNewReservationUsesTwoHoursInUTC(t *testing.T) {
	start := time.Date(2026, 11, 14, 20, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	got, err := NewReservation(1, Table{ID: 3, Capacity: 4}, Request{3, 4, start}, start)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID() != 1 || got.TableID() != 3 || got.GuestCount() != 4 {
		t.Fatalf("unexpected reservation: %+v", got)
	}
	if got.StartsAt() != start.UTC() || got.EndsAt() != start.UTC().Add(2*time.Hour) {
		t.Fatalf("unexpected interval: [%s, %s)", got.StartsAt(), got.EndsAt())
	}
}

func TestNewReservationRejectsInvalidRequests(t *testing.T) {
	now := time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)
	table := Table{ID: 3, Capacity: 4}
	tests := []struct {
		name    string
		userID  int64
		request Request
		want    error
	}{
		{"unknown user", 0, Request{3, 4, now}, ErrInvalidUser},
		{"missing table", 1, Request{0, 4, now}, ErrInvalidTable},
		{"different table", 1, Request{5, 4, now}, ErrInvalidTable},
		{"no guests", 1, Request{3, 0, now}, ErrInvalidParty},
		{"too many guests", 1, Request{3, 5, now}, ErrInvalidParty},
		{"missing start", 1, Request{3, 4, time.Time{}}, ErrInvalidStart},
		{"past start", 1, Request{3, 4, now.Add(-time.Second)}, ErrInvalidStart},
		{"end outside RFC 3339", 1, Request{3, 4, time.Date(9999, 12, 31, 23, 0, 0, 0, time.UTC)}, ErrInvalidStart},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewReservation(test.userID, table, test.request, now)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestReservationConflictsWith(t *testing.T) {
	now := time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)
	first := makeReservation(t, 1, 3, now, now)
	tests := []struct {
		name    string
		tableID int64
		offset  time.Duration
		want    bool
	}{
		{"same table and start", 3, 0, true},
		{"overlapping start", 3, time.Hour, true},
		{"adjacent interval", 3, 2 * time.Hour, false},
		{"different table", 5, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			other := makeReservation(t, 2, test.tableID, now.Add(test.offset), now)
			if first.ConflictsWith(other) != test.want || other.ConflictsWith(first) != test.want {
				t.Fatalf("expected conflict in both directions: %t", test.want)
			}
		})
	}
}

func makeReservation(t *testing.T, userID, tableID int64, start, now time.Time) Reservation {
	t.Helper()
	reservation, err := NewReservation(userID, Table{ID: tableID, Capacity: 4}, Request{tableID, 2, start}, now)
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}
