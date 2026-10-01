// Package config loads settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the application configuration.
type Config struct {
	Port            string
	DatabaseURL     string
	JWTSecret       string
	JWTTTL          time.Duration
	Location        *time.Location
	CORSOrigins     string
	TrustProxy      bool
	SeedDemoStaff   bool
	AdminEmail      string
	AdminPassword   string
	StaffPassword   string
	TokenRatePerMin int
	LoginRatePerMin int
	WebDir          string
}

// Load reads the configuration, applying development defaults where sensible.
func Load() (*Config, error) {
	tzName := env("APP_TIMEZONE", "Asia/Kolkata")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("APP_TIMEZONE %q: %w", tzName, err)
	}
	c := &Config{
		Port:            env("PORT", "8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/smartqueue?sslmode=disable"),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		JWTTTL:          time.Duration(envInt("JWT_TTL_HOURS", 12)) * time.Hour,
		Location:        loc,
		CORSOrigins:     env("CORS_ORIGINS", "*"),
		TrustProxy:      envBool("TRUST_PROXY", false),
		SeedDemoStaff:   envBool("SEED_DEMO_STAFF", true),
		AdminEmail:      env("ADMIN_EMAIL", "admin@office.local"),
		AdminPassword:   env("ADMIN_PASSWORD", "Admin@123"),
		StaffPassword:   env("STAFF_PASSWORD", "Staff@123"),
		TokenRatePerMin: envInt("TOKEN_RATE_PER_MIN", 20),
		LoginRatePerMin: envInt("LOGIN_RATE_PER_MIN", 10),
		WebDir:          os.Getenv("WEB_DIR"),
	}
	if c.JWTSecret == "" {
		c.JWTSecret = "dev-only-insecure-secret-change-me"
		slog.Warn("JWT_SECRET is not set; using an insecure development secret")
	} else if len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	return c, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
