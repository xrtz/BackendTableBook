package httpapi

import (
	"net/http"
	"strconv"
	"time"
	_ "time/tzdata"

	"tablebook/internal/booking"
)

func (h handler) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.authenticate(r)
	if !ok || principal.UserID <= 0 {
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	role := "guest"
	if principal.Admin {
		role = "admin"
	}
	writeJSON(w, http.StatusOK, map[string]string{"role": role})
}

func (h handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.authenticate(r)
	if !ok || principal.UserID <= 0 {
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return false
	}
	if !principal.Admin {
		writeCode(w, http.StatusForbidden, "FORBIDDEN")
		return false
	}
	return true
}

func (h handler) adminJournal(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	query := r.URL.Query()
	hallID, err := strconv.ParseInt(query.Get("hallId"), 10, 64)
	status := query.Get("status")
	if err != nil || hallID <= 0 || (status != "" && !knownStatus(status)) {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	plan, err := h.store.FloorPlan(r.Context(), hallID)
	if err != nil {
		writeError(w, err)
		return
	}
	zone, err := time.LoadLocation(plan.TimeZone)
	if err != nil {
		writeError(w, err)
		return
	}
	day, err := time.ParseInLocation(time.DateOnly, query.Get("day"), zone)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	records, err := h.store.AdminReservations(r.Context(), booking.AdminFilter{
		HallID: hallID, From: day, To: day.AddDate(0, 0, 1), Status: status,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	type journalEntry struct {
		reservationResponse
		TableLabel string `json:"tableLabel"`
		GuestEmail string `json:"guestEmail"`
	}
	result := make([]journalEntry, 0, len(records))
	for _, record := range records {
		result = append(result, journalEntry{reservationResponse(record.Snapshot), record.TableLabel, record.GuestEmail})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h handler) adminCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !booking.ValidKey(keys[0]) {
		writeError(w, booking.ErrInvalidKey)
		return
	}
	var input struct {
		reservationSchedule
		Email      string `json:"email"`
		GuestCount int    `json:"guestCount"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	reservation, err := h.store.CreateForGuest(r.Context(), input.Email, keys[0], booking.Request{
		TableID: input.TableID, GuestCount: input.GuestCount, StartsAt: input.StartsAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reservationResponse(reservation))
}

func (h handler) adminStatus(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	var input struct {
		Status          string `json:"status"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !knownStatus(input.Status) || input.Status == "confirmed" {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if input.ExpectedVersion <= 0 {
		writeError(w, booking.ErrInvalidVersion)
		return
	}
	if err := h.store.ChangeStatus(r.Context(), id, input.ExpectedVersion, input.Status); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func knownStatus(status string) bool {
	switch status {
	case "confirmed", "seated", "completed", "cancelled":
		return true
	default:
		return false
	}
}
