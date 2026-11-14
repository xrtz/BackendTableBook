package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tablebook/internal/httpapi"
	"tablebook/internal/postgres"
)

type reply struct {
	status int
	body   string
}

type reservation struct {
	ID      int64
	TableID int64
	Version int64
}

func TestPostgresBookingAPI(t *testing.T) {
	pool := testDatabase(t)
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	handler := httpapi.New(postgres.New(pool, func() time.Time { return now }), func(r *http.Request) (httpapi.Principal, bool) {
		switch httpapi.BearerToken(r) {
		case "guest":
			return httpapi.Principal{UserID: 1}, true
		case "other-guest":
			return httpapi.Principal{UserID: 2}, true
		case "admin":
			return httpapi.Principal{UserID: 3, Admin: true}, true
		default:
			return httpapi.Principal{}, false
		}
	})
	send := func(method, path, token, key, body string) reply {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		r := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return reply{w.Code, w.Body.String()}
	}
	create := func(token, key string, table int, startsAt string) reply {
		body := fmt.Sprintf(`{"tableId":%d,"guestCount":2,"startsAt":%q}`, table, startsAt)
		return send(http.MethodPost, "/v1/reservations", token, key, body)
	}
	const start = "2026-11-14T19:00:00Z"
	const adjacent = "2026-11-14T21:00:00Z"
	require(t, send(http.MethodGet, "/v1/halls/1/floor-plan", "", "", ""), 200, `"shape":"round"`)
	require(t, create("", "unauth", 1, start), 401, "UNAUTHENTICATED")

	type attempt struct {
		token string
		reply reply
	}
	gate := make(chan struct{})
	attempts := make(chan attempt, 2)
	for _, token := range []string{"guest", "other-guest"} {
		go func() {
			<-gate
			attempts <- attempt{token, create(token, "same-key", 1, start)}
		}()
	}
	close(gate)
	winner, loser := <-attempts, <-attempts
	if loser.reply.status == 201 {
		winner, loser = loser, winner
	}
	require(t, winner.reply, 201, `"status":"confirmed"`)
	require(t, loser.reply, 409, "TABLE_UNAVAILABLE")
	if replay := create(winner.token, "same-key", 1, "2026-11-14T22:00:00+03:00"); replay != winner.reply {
		t.Fatalf("different replay: %+v, original: %+v", replay, winner.reply)
	}
	require(t, create(winner.token, "same-key", 1, adjacent), 409, "IDEMPOTENCY_KEY_REUSED")
	require(t, create(loser.token, "same-key", 1, "2026-11-14T20:00:00Z"), 409, "TABLE_UNAVAILABLE")
	require(t, create(loser.token, "same-key", 1, adjacent), 201, `"status":"confirmed"`)

	parallel := make(chan reply, 2)
	for range 2 {
		go func() { parallel <- create("guest", "parallel-key", 2, start) }()
	}
	otherTable, replay := <-parallel, <-parallel
	require(t, otherTable, 201, `"tableId":2`)
	if replay != otherTable {
		t.Fatalf("concurrent replay differs: %+v and %+v", otherTable, replay)
	}
	var moved reservation
	if err := json.Unmarshal([]byte(otherTable.body), &moved); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/v1/admin/reservations/%d/schedule", moved.ID)
	move := func(token string, tableID int) reply {
		return send(http.MethodPatch, path, token, "",
			fmt.Sprintf(`{"tableId":%d,"expectedVersion":1,"startsAt":%q}`, tableID, start))
	}
	require(t, move("guest", 1), 403, "FORBIDDEN")
	require(t, move("admin", 1), 409, "TABLE_UNAVAILABLE")
	var actualTable, actualVersion int64
	if err := pool.QueryRow(t.Context(), "SELECT table_id, version FROM reservations WHERE id=$1", moved.ID).
		Scan(&actualTable, &actualVersion); err != nil {
		t.Fatal(err)
	}
	if actualTable != 2 || actualVersion != 1 {
		t.Fatalf("conflicting move changed table to %d or version to %d", actualTable, actualVersion)
	}
	require(t, move("admin", 3), 200, `"version":2`)
	require(t, move("admin", 4), 409, "STALE_RESERVATION")
	if replay := create("guest", "parallel-key", 2, start); replay != otherTable {
		t.Fatalf("move changed original idempotent response: %+v", replay)
	}

	// Exercise the exclusion constraint without the application's table lock.
	_, err := pool.Exec(t.Context(), `
		INSERT INTO reservations (user_id, table_id, guest_count, starts_at, ends_at)
		VALUES (9, 1, 2, '2026-11-14 19:00:00Z', '2026-11-14 21:00:00Z')`)
	if pgError, ok := err.(interface{ SQLState() string }); !ok || pgError.SQLState() != "23P01" {
		t.Fatalf("direct overlapping insert: got %v, want 23P01", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE reservations SET status='cancelled' WHERE table_id=1 AND starts_at=$1",
		time.Date(2026, 11, 14, 19, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	require(t, create("guest", "after-cancellation", 1, start), 201, `"tableId":1`)
}

func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TABLEBOOK_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TABLEBOOK_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("tablebook_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"001_booking.sql", "demo_seed.sql"} {
		script, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(script)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool
}

func require(t *testing.T, result reply, status int, fragment string) {
	t.Helper()
	if result.status != status || !strings.Contains(result.body, fragment) {
		t.Fatalf("got %d %s, want %d containing %s", result.status, result.body, status, fragment)
	}
}
