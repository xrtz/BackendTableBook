package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"tablebook/internal/booking"
)

type hallResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	TimeZone string `json:"timeZone"`
}

func (h handler) halls(w http.ResponseWriter, r *http.Request) {
	halls, err := h.store.Halls(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]hallResponse, 0, len(halls))
	for _, hall := range halls {
		result = append(result, hallResponse(hall))
	}
	writeJSON(w, http.StatusOK, result)
}

type availabilityResponse struct {
	TableID   int64 `json:"tableId"`
	Available bool  `json:"available"`
}

func (h handler) availability(w http.ResponseWriter, r *http.Request) {
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	start, startErr := time.Parse(time.RFC3339Nano, r.URL.Query().Get("startsAt"))
	guests, guestsErr := strconv.ParseInt(r.URL.Query().Get("guestCount"), 10, 32)
	if idErr != nil || id <= 0 || startErr != nil || guestsErr != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	tables, err := h.store.Availability(r.Context(), id, booking.AvailabilityQuery{StartsAt: start, GuestCount: int(guests)})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]availabilityResponse, 0, len(tables))
	for _, table := range tables {
		result = append(result, availabilityResponse(table))
	}
	writeJSON(w, http.StatusOK, result)
}

type ownedReservationResponse struct {
	reservationResponse
	TableLabel string `json:"tableLabel"`
	HallName   string `json:"hallName"`
	TimeZone   string `json:"timeZone"`
}

func (h handler) listReservations(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(r)
	if !ok || principal.UserID <= 0 {
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	reservations, err := h.store.ListForOwner(r.Context(), principal.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]ownedReservationResponse, 0, len(reservations))
	for _, r := range reservations {
		result = append(result, ownedReservationResponse{reservationResponse(r.Snapshot), r.TableLabel, r.HallName, r.TimeZone})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h handler) cancelReservation(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(r)
	if !ok || principal.UserID <= 0 {
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if err := h.store.CancelForOwner(r.Context(), principal.UserID, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
