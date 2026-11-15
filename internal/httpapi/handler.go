package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tablebook/internal/booking"
)

type Principal struct {
	UserID int64
	Admin  bool
}

type Authenticate func(*http.Request) (Principal, bool)

type handler struct {
	store        booking.Store
	authenticate Authenticate
}

func New(store booking.Store, authenticate Authenticate) http.Handler {
	api := handler{store: store, authenticate: authenticate}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/halls", api.halls)
	mux.HandleFunc("GET /v1/halls/{id}/floor-plan", api.floorPlan)
	mux.HandleFunc("GET /v1/halls/{id}/availability", api.availability)
	mux.HandleFunc("POST /v1/reservations", api.create)
	mux.HandleFunc("GET /v1/reservations", api.listReservations)
	mux.HandleFunc("GET /v1/reservations/{id}", api.getReservation)
	mux.HandleFunc("POST /v1/reservations/{id}/cancel", api.cancelReservation)
	mux.HandleFunc("GET /v1/auth/me", api.me)
	mux.HandleFunc("GET /v1/admin/reservations", api.adminJournal)
	mux.HandleFunc("POST /v1/admin/reservations", api.adminCreate)
	mux.HandleFunc("POST /v1/admin/reservations/{id}/status", api.adminStatus)
	mux.HandleFunc("PATCH /v1/admin/reservations/{id}/schedule", api.move)
	mux.HandleFunc("POST /v1/admin/reservations/{id}/schedule", api.move)
	return mux
}

func (h handler) getReservation(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.store.GetForOwner(r.Context(), principal.UserID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse(result))
}

type reservationResponse struct {
	ID         int64     `json:"id"`
	TableID    int64     `json:"tableId"`
	GuestCount int       `json:"guestCount"`
	StartsAt   time.Time `json:"startsAt"`
	EndsAt     time.Time `json:"endsAt"`
	Status     string    `json:"status"`
	Version    int64     `json:"version"`
}

type reservationSchedule struct {
	TableID  int64     `json:"tableId"`
	StartsAt time.Time `json:"startsAt"`
}

func (h handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(r)
	if !ok || principal.UserID <= 0 {
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !booking.ValidKey(keys[0]) {
		writeError(w, booking.ErrInvalidKey)
		return
	}
	var input struct {
		reservationSchedule
		GuestCount int `json:"guestCount"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	result, err := h.store.Create(r.Context(), principal.UserID, keys[0], booking.Request{
		TableID: input.TableID, GuestCount: input.GuestCount, StartsAt: input.StartsAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reservationResponse(result))
}

func (h handler) move(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	var input struct {
		reservationSchedule
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	result, err := h.store.Move(r.Context(), booking.MoveRequest{
		ReservationID: id, TableID: input.TableID, StartsAt: input.StartsAt, ExpectedVersion: input.ExpectedVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse(result))
}

type floorTableResponse struct {
	ID       int64  `json:"id"`
	Label    string `json:"label"`
	Capacity int    `json:"capacity"`
	Shape    string `json:"shape"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Size     int    `json:"size"`
}

func (h handler) floorPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	plan, err := h.store.FloorPlan(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	tables := make([]floorTableResponse, 0, len(plan.Tables))
	for _, table := range plan.Tables {
		tables = append(tables, floorTableResponse(table))
	}
	writeJSON(w, http.StatusOK, struct {
		ID         int64                `json:"id"`
		Name       string               `json:"name"`
		TimeZone   string               `json:"timeZone"`
		Width      int                  `json:"width"`
		Height     int                  `json:"height"`
		Tables     []floorTableResponse `json:"tables"`
		ServerTime time.Time            `json:"serverTime"`
	}{plan.ID, plan.Name, plan.TimeZone, plan.Width, plan.Height, tables, plan.ServerTime})
}

func readJSON(w http.ResponseWriter, r *http.Request, input any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, booking.ErrGuestNotFound):
		writeCode(w, http.StatusNotFound, "GUEST_NOT_FOUND")
	case errors.Is(err, booking.ErrCannotCancel):
		writeCode(w, http.StatusConflict, "RESERVATION_CANNOT_CANCEL")
	case errors.Is(err, booking.ErrTableUnavailable):
		writeCode(w, http.StatusConflict, "TABLE_UNAVAILABLE")
	case errors.Is(err, booking.ErrKeyReused):
		writeCode(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")
	case errors.Is(err, booking.ErrStaleReservation):
		writeCode(w, http.StatusConflict, "STALE_RESERVATION")
	case errors.Is(err, booking.ErrNotFound):
		writeCode(w, http.StatusNotFound, "RESERVATION_NOT_FOUND")
	case errors.Is(err, booking.ErrHallNotFound):
		writeCode(w, http.StatusNotFound, "HALL_NOT_FOUND")
	case errors.Is(err, booking.ErrInvalidTable):
		writeCode(w, http.StatusBadRequest, "INVALID_TABLE")
	case errors.Is(err, booking.ErrInvalidParty):
		writeCode(w, http.StatusBadRequest, "INVALID_GUEST_COUNT")
	case errors.Is(err, booking.ErrInvalidStart):
		writeCode(w, http.StatusBadRequest, "INVALID_START_TIME")
	case errors.Is(err, booking.ErrInvalidKey):
		writeCode(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY")
	case errors.Is(err, booking.ErrInvalidVersion):
		writeCode(w, http.StatusBadRequest, "INVALID_VERSION")
	default:
		log.Printf("booking request failed: %v", err)
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
	}
}

func writeCode(w http.ResponseWriter, status int, code string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func BearerToken(r *http.Request) string {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}
