package accounts

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrInvalidRegistration = errors.New("invalid registration fields")
	ErrEmailTaken          = errors.New("email already registered")
	ErrUnauthenticated     = errors.New("invalid or expired session")
)

type Principal struct {
	UserID int64
	Admin  bool
}

type Tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

type Service struct {
	pool      *pgxpool.Pool
	now       func() time.Time
	hashSlots chan struct{}
}

func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	return &Service{pool: pool, now: now, hashSlots: make(chan struct{}, 4)}
}

func (s *Service) Register(ctx context.Context, email, password string) (Tokens, error) {
	email = normalizeEmail(email)
	if !validEmail(email) || len(password) > 512 || utf8.RuneCountInString(password) < 15 {
		return Tokens{}, ErrInvalidRegistration
	}
	salt := randBytes()
	hash, err := s.hashPassword(ctx, password, salt)
	if err != nil {
		return Tokens{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var id int64
	err = tx.QueryRow(ctx, "INSERT INTO accounts (email, password_salt, password_hash) VALUES ($1, $2, $3) RETURNING id",
		email, salt, hash).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "accounts_email_key" {
		return Tokens{}, ErrEmailTaken
	}
	if err != nil {
		return Tokens{}, err
	}
	tokens, err := s.issue(ctx, tx, id)
	if err != nil {
		return Tokens{}, err
	}
	return tokens, tx.Commit(ctx)
}

func (s *Service) Login(ctx context.Context, email, password string) (Tokens, error) {
	email = normalizeEmail(email)
	if !validEmail(email) || len(password) > 512 {
		return Tokens{}, ErrInvalidCredentials
	}
	var id int64
	salt := make([]byte, 32)
	expected := make([]byte, 32)
	err := s.pool.QueryRow(ctx, "SELECT id, password_salt, password_hash FROM accounts WHERE email = $1", email).
		Scan(&id, &salt, &expected)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, err
	}
	actual, err := s.hashPassword(ctx, password, salt)
	if err != nil {
		return Tokens{}, err
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 || id == 0 {
		return Tokens{}, ErrInvalidCredentials
	}
	return s.issue(ctx, s.pool, id)
}

type sessionWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *Service) issue(ctx context.Context, db sessionWriter, accountID int64) (Tokens, error) {
	tokens := newTokens()
	now := s.now()
	_, err := db.Exec(ctx, `
		INSERT INTO sessions (access_hash, refresh_hash, account_id, access_expires_at, refresh_expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		tokenHash(tokens.AccessToken), tokenHash(tokens.RefreshToken), accountID, now.Add(15*time.Minute), now.Add(30*24*time.Hour))
	return tokens, err
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if !validToken(refreshToken) {
		return Tokens{}, ErrUnauthenticated
	}
	tokens := newTokens()
	now := s.now()
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET access_hash = $2, refresh_hash = $3,
			access_expires_at = LEAST($4, refresh_expires_at)
		WHERE refresh_hash = $1 AND refresh_expires_at > $5`,
		tokenHash(refreshToken), tokenHash(tokens.AccessToken), tokenHash(tokens.RefreshToken), now.Add(15*time.Minute), now)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrUnauthenticated
	}
	return tokens, err
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if !validToken(refreshToken) {
		return nil
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE refresh_hash = $1", tokenHash(refreshToken))
	return err
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if !validToken(token) {
		return Principal{}, ErrUnauthenticated
	}
	var granted Principal
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.role = 'admin' FROM sessions s JOIN accounts a ON a.id = s.account_id
		WHERE s.access_hash = $1 AND s.access_expires_at > $2`, tokenHash(token), s.now()).
		Scan(&granted.UserID, &granted.Admin)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthenticated
	}
	return granted, err
}

func (s *Service) hashPassword(ctx context.Context, password string, salt []byte) ([]byte, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return pbkdf2.Key(sha256.New, password, salt, 600_000, 32)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 254
}

func newTokens() Tokens {
	return Tokens{
		AccessToken:  base64.RawURLEncoding.EncodeToString(randBytes()),
		RefreshToken: base64.RawURLEncoding.EncodeToString(randBytes()),
	}
}

func randBytes() []byte {
	var result [32]byte
	rand.Read(result[:])
	return result[:]
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func validToken(token string) bool {
	return len(token) == 43
}
