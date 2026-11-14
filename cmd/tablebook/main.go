package main

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tablebook/internal/httpapi"
	"tablebook/internal/postgres"
)

func main() {
	dsn := os.Getenv("TABLEBOOK_DATABASE_URL")
	guest := os.Getenv("TABLEBOOK_DEMO_GUEST_TOKEN")
	admin := os.Getenv("TABLEBOOK_DEMO_ADMIN_TOKEN")
	if dsn == "" || !validToken(guest) || !validToken(admin) || guest == admin {
		log.Fatal("set TABLEBOOK_DATABASE_URL and distinct demo guest/admin tokens (at least 16 characters, no whitespace)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal("database connection failed")
	}
	cancel()
	authenticate := func(r *http.Request) (httpapi.Principal, bool) {
		token := httpapi.BearerToken(r)
		switch {
		case subtle.ConstantTimeCompare([]byte(token), []byte(guest)) == 1:
			return httpapi.Principal{UserID: 1}, true
		case subtle.ConstantTimeCompare([]byte(token), []byte(admin)) == 1:
			return httpapi.Principal{UserID: 2, Admin: true}, true
		default:
			return httpapi.Principal{}, false
		}
	}
	server := http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           httpapi.New(postgres.New(pool, time.Now), authenticate),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("TableBook demo listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func validToken(token string) bool {
	return len(token) >= 16 && !strings.ContainsAny(token, " \t\r\n")
}
