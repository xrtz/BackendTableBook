package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tablebook/internal/accounts"
	"tablebook/internal/httpapi"
	"tablebook/internal/postgres"
)

func main() {
	dsn := os.Getenv("TABLEBOOK_DATABASE_URL")
	if dsn == "" {
		log.Fatal("set TABLEBOOK_DATABASE_URL")
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
	store := postgres.New(pool, time.Now)
	sessions := accounts.New(pool, time.Now)
	server := http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           httpapi.WithAccounts(store, sessions),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("TableBook demo listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
