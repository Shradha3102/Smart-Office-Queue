// Package seed inserts the default departments and demo accounts on first start.
package seed

import (
	"context"
	"database/sql"
	"log/slog"

	"smartqueue/internal/auth"
)

type dept struct {
	code, name, desc, staffEmail string
	avg, order                   int
}

var defaultDepartments = []dept{
	{"IT", "IT Support", "Laptops, accounts, network and software help", "it@office.local", 6, 1},
	{"HR", "HR", "Leave, payroll queries, onboarding and documents", "hr@office.local", 8, 2},
	{"ACC", "Accounts", "Reimbursements, invoices and payments", "accounts@office.local", 7, 3},
	{"ADM", "Administration", "ID cards, access, facilities and general admin", "administration@office.local", 5, 4},
}

// Options controls seeding.
type Options struct {
	AdminEmail    string
	AdminPassword string
	DemoStaff     bool   // create one staff login per department
	StaffPassword string // password for demo staff
}

// Run seeds departments (always, if missing) and accounts (if no users exist).
func Run(ctx context.Context, db *sql.DB, opt Options) error {
	for _, d := range defaultDepartments {
		if _, err := db.ExecContext(ctx, `INSERT INTO departments (code, name, description, avg_service_minutes, sort_order)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (code) DO NOTHING`, d.code, d.name, d.desc, d.avg, d.order); err != nil {
			return err
		}
	}

	var users int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_users`).Scan(&users); err != nil {
		return err
	}
	if users > 0 {
		return nil
	}
	hash, err := auth.HashPassword(opt.AdminPassword)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO staff_users (name, email, password_hash, role) VALUES ('Administrator', $1, $2, 'admin')`,
		opt.AdminEmail, hash); err != nil {
		return err
	}
	slog.Info("created admin account", "email", opt.AdminEmail)

	if !opt.DemoStaff {
		return nil
	}
	staffHash, err := auth.HashPassword(opt.StaffPassword)
	if err != nil {
		return err
	}
	for _, d := range defaultDepartments {
		if _, err := db.ExecContext(ctx, `INSERT INTO staff_users (name, email, password_hash, role, department_id)
			SELECT $1, $2, $3, 'staff', id FROM departments WHERE code = $4`, d.name+" Desk", d.staffEmail, staffHash, d.code); err != nil {
			return err
		}
		slog.Info("created demo staff account", "email", d.staffEmail, "department", d.name)
	}
	return nil
}
