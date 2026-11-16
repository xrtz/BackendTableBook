package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"tablebook/internal/accounts"
	"tablebook/internal/httpapi"
)

type change struct {
	Owner   int64 `json:"owner"`
	Hall    int64 `json:"hall"`
	OldHall int64 `json:"oldHall"`
}

type subscriber struct {
	owner  int64
	hall   int64
	signal chan struct{}
}

type BookingEvents struct {
	mu          sync.Mutex
	subscribers map[*subscriber]struct{}
}

func Listen(ctx context.Context, pool *pgxpool.Pool) *BookingEvents {
	events := &BookingEvents{subscribers: make(map[*subscriber]struct{})}
	go events.receive(ctx, pool)
	return events
}

func (e *BookingEvents) receive(ctx context.Context, pool *pgxpool.Pool) {
	for ctx.Err() == nil {
		if err := e.listenOnce(ctx, pool); err != nil && ctx.Err() == nil {
			log.Printf("booking events listener: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func (e *BookingEvents) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// A LISTEN session must never go back into the query pool.
		physical := conn.Hijack()
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = physical.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN tablebook_booking"); err != nil {
		return err
	}
	e.publish(change{})
	for {
		message, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var event change
		if err := json.Unmarshal([]byte(message.Payload), &event); err == nil && event.Owner > 0 {
			e.publish(event)
		}
	}
}

func (e *BookingEvents) publish(event change) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for client := range e.subscribers {
		if event.Owner == 0 || event.Owner == client.owner ||
			(client.hall != 0 && (event.Hall == client.hall || event.OldHall == client.hall)) {
			select {
			case client.signal <- struct{}{}:
			default:
			}
		}
	}
}

type authenticate func(context.Context, string) (accounts.Principal, error)

func (e *BookingEvents) Handler(auth authenticate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := httpapi.BearerToken(r)
		user, err := auth(r.Context(), token)
		if err != nil {
			if errors.Is(err, accounts.ErrUnauthenticated) {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
			} else {
				http.Error(w, "session unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		var hall int64
		if value := r.URL.Query().Get("hallId"); value != "" {
			hall, err = strconv.ParseInt(value, 10, 64)
			if err != nil || hall < 1 {
				http.Error(w, "invalid hall", http.StatusBadRequest)
				return
			}
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := conn.CloseRead(r.Context())
		client := &subscriber{owner: user.UserID, hall: hall, signal: make(chan struct{}, 1)}
		e.mu.Lock()
		e.subscribers[client] = struct{}{}
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			delete(e.subscribers, client)
			e.mu.Unlock()
		}()
		client.signal <- struct{}{}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !checkSession(ctx, conn, token, auth) {
					return
				}
			case <-client.signal:
				if !checkSession(ctx, conn, token, auth) {
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"bookings_changed"}`))
				cancel()
				if err != nil {
					return
				}
			}
		}
	}
}

func checkSession(ctx context.Context, conn *websocket.Conn, token string, auth authenticate) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := auth(checkCtx, token); err != nil {
		if errors.Is(err, accounts.ErrUnauthenticated) {
			_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
		} else {
			_ = conn.Close(websocket.StatusInternalError, "session unavailable")
		}
		return false
	}
	return true
}
