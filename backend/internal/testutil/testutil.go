// Package testutil provisions throw-away PostgreSQL databases for integration tests.
//
// Set TEST_DATABASE_URL to a server/user that may CREATE DATABASE, e.g.
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable go test ./...
//
// Tests are skipped when it is not set.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"smartqueue/internal/auth"
	"smartqueue/internal/db"
	"smartqueue/internal/seed"
)

// Fixture holds a migrated, seeded database and handy ids.
type Fixture struct {
	DB    *sql.DB
	Depts map[string]int // code -> id
	Admin auth.Actor
	Staff map[string]auth.Actor // dept code -> staff actor
}

// New creates an isolated database for the test and drops it afterwards.
func New(t *testing.T) *Fixture {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()

	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "sq_test_" + hex.EncodeToString(b)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		_ = admin.Close()
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(ctx, pool, seed.Options{
		AdminEmail: "admin@test.local", AdminPassword: "Admin@123", DemoStaff: true, StaffPassword: "Staff@123",
	}); err != nil {
		t.Fatal(err)
	}

	f := &Fixture{DB: pool, Depts: map[string]int{}, Staff: map[string]auth.Actor{}}
	rows, err := pool.QueryContext(ctx, `SELECT id, code FROM departments`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int
		var code string
		_ = rows.Scan(&id, &code)
		f.Depts[code] = id
	}
	rows.Close()

	rows, err = pool.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.role, u.department_id, d.code
		FROM staff_users u LEFT JOIN departments d ON d.id = u.department_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			a    auth.Actor
			dept sql.NullInt64
			code sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.Role, &dept, &code); err != nil {
			t.Fatal(err)
		}
		if a.Role == auth.RoleAdmin {
			f.Admin = a
			continue
		}
		d := int(dept.Int64)
		a.DepartmentID = &d
		f.Staff[code.String] = a
	}
	return f
}
