package httpapi

import (
	"context"
	"errors"
	"net/http"

	"tablebook/internal/accounts"
	"tablebook/internal/booking"
)

type Sessions interface {
	Register(context.Context, string, string) (accounts.Tokens, error)
	Login(context.Context, string, string) (accounts.Tokens, error)
	Refresh(context.Context, string) (accounts.Tokens, error)
	Logout(context.Context, string) error
	Authenticate(context.Context, string) (accounts.Principal, error)
}

func WithAccounts(store booking.Store, sessions Sessions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", New(store, func(r *http.Request) (Principal, bool) {
		account, err := sessions.Authenticate(r.Context(), BearerToken(r))
		return Principal(account), err == nil
	}))
	mux.HandleFunc("POST /v1/auth/register", credentials(sessions.Register, http.StatusCreated))
	mux.HandleFunc("POST /v1/auth/login", credentials(sessions.Login, http.StatusOK))
	mux.HandleFunc("POST /v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		var input refreshRequest
		if readJSON(w, r, &input) {
			result, err := sessions.Refresh(r.Context(), input.RefreshToken)
			writeSession(w, http.StatusOK, result, err)
		}
	})
	mux.HandleFunc("POST /v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		var input refreshRequest
		if readJSON(w, r, &input) {
			if err := sessions.Logout(r.Context(), input.RefreshToken); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	return mux
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func credentials(run func(context.Context, string, string) (accounts.Tokens, error), status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if readJSON(w, r, &input) {
			result, err := run(r.Context(), input.Email, input.Password)
			writeSession(w, status, result, err)
		}
	}
}

func writeSession(w http.ResponseWriter, status int, result accounts.Tokens, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case err == nil:
		writeJSON(w, status, result)
	case errors.Is(err, accounts.ErrInvalidCredentials):
		writeCode(w, http.StatusUnauthorized, "INVALID_CREDENTIALS")
	case errors.Is(err, accounts.ErrUnauthenticated):
		writeCode(w, http.StatusUnauthorized, "UNAUTHENTICATED")
	case errors.Is(err, accounts.ErrInvalidRegistration):
		writeCode(w, http.StatusBadRequest, "INVALID_REGISTRATION")
	case errors.Is(err, accounts.ErrEmailTaken):
		writeCode(w, http.StatusConflict, "EMAIL_TAKEN")
	default:
		writeError(w, err)
	}
}
