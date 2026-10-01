// Package auth handles staff credentials, JWT issuing/verification and request identity.
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"smartqueue/internal/apperr"
)

const (
	RoleAdmin = "admin"
	RoleStaff = "staff"

	pbkdf2Iterations = 210_000 // OWASP 2023 guidance for PBKDF2-HMAC-SHA512
	saltLen          = 16
	keyLen           = 32
)

// Actor is the authenticated staff member performing a request.
type Actor struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	DepartmentID *int   `json:"department_id"`
}

// IsAdmin reports whether the actor has full access.
func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }

// CanManage reports whether the actor may operate the given department's queue.
func (a Actor) CanManage(departmentID int) bool {
	if a.IsAdmin() {
		return true
	}
	return a.DepartmentID != nil && *a.DepartmentID == departmentID
}

// HashPassword returns an encoded PBKDF2-SHA512 hash: pbkdf2-sha512$iter$salt$key.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha512New, password, salt, pbkdf2Iterations, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha512$%d$%s$%s", pbkdf2Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword verifies password against an encoded hash in constant time.
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha512" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha512New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// HashSecret returns a hex SHA-256 digest (used for visitor token secrets, which are high-entropy).
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return fmt.Sprintf("%x", sum[:])
}

// NewSecret returns a random URL-safe secret.
func NewSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// claims is the JWT payload.
type claims struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	DepartmentID *int   `json:"dept,omitempty"`
	jwt.RegisteredClaims
}

// Service issues and verifies JWTs and authenticates staff.
type Service struct {
	db     *sql.DB
	secret []byte
	ttl    time.Duration
}

// NewService builds an auth Service.
func NewService(db *sql.DB, secret string, ttl time.Duration) *Service {
	return &Service{db: db, secret: []byte(secret), ttl: ttl}
}

// LoginResult is returned on successful login.
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      Actor     `json:"user"`
}

// Login authenticates by email/password.
func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, apperr.BadRequest(apperr.CodeValidation, "Email and password are required")
	}
	var (
		a        Actor
		hash     string
		active   bool
		deptNull sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, name, email, role, department_id, password_hash, is_active
		FROM staff_users WHERE lower(email) = lower($1)`, email).
		Scan(&a.ID, &a.Name, &a.Email, &a.Role, &deptNull, &hash, &active)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable CPU so response time does not reveal which emails exist.
		CheckPassword(dummyHash, password)
		return nil, apperr.Unauthorized("Invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(hash, password) || !active {
		return nil, apperr.Unauthorized("Invalid email or password")
	}
	if deptNull.Valid {
		d := int(deptNull.Int64)
		a.DepartmentID = &d
	}
	tok, exp, err := s.Issue(a)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: tok, ExpiresAt: exp, User: a}, nil
}

// Issue signs a JWT for the actor.
func (s *Service) Issue(a Actor) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(s.ttl)
	c := claims{
		Name: a.Name, Email: a.Email, Role: a.Role, DepartmentID: a.DepartmentID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.Itoa(a.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "smart-queue",
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	return signed, exp, err
}

// Verify parses and validates a JWT.
func (s *Service) Verify(token string) (Actor, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("smart-queue"))
	if err != nil {
		return Actor{}, apperr.Unauthorized("Session expired or invalid, please log in again")
	}
	id, err := strconv.Atoi(c.Subject)
	if err != nil {
		return Actor{}, apperr.Unauthorized("Invalid token subject")
	}
	return Actor{ID: id, Name: c.Name, Email: c.Email, Role: c.Role, DepartmentID: c.DepartmentID}, nil
}

// IsActive confirms the user still exists and is enabled (checked on each staff request).
func (s *Service) IsActive(ctx context.Context, id int) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT is_active FROM staff_users WHERE id=$1`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}

type ctxKey struct{}

// WithActor stores the actor on the context.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// ActorFrom returns the actor from the context.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(Actor)
	return a, ok
}

var dummyHash = func() string {
	h, _ := HashPassword("timing-equaliser")
	return h
}()
