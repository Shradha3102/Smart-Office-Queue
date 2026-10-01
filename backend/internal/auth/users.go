package auth

import (
	"context"
	"database/sql"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"

	"smartqueue/internal/apperr"
)

// StaffUser is a staff/admin account as listed to administrators.
type StaffUser struct {
	ID             int       `json:"id"`
	Name           string    `json:"name"`
	Email          string    `json:"email"`
	Role           string    `json:"role"`
	DepartmentID   *int      `json:"department_id"`
	DepartmentName *string   `json:"department_name"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

// CreateUserInput is used by admins to add staff.
type CreateUserInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	Role         string `json:"role"`
	DepartmentID *int   `json:"department_id"`
}

// ListUsers returns all staff accounts.
func (s *Service) ListUsers(ctx context.Context) ([]StaffUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.role, u.department_id, d.name, u.is_active, u.created_at
		FROM staff_users u LEFT JOIN departments d ON d.id = u.department_id ORDER BY u.role, u.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StaffUser{}
	for rows.Next() {
		var (
			u     StaffUser
			dept  sql.NullInt64
			dname sql.NullString
		)
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &dept, &dname, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		if dept.Valid {
			d := int(dept.Int64)
			u.DepartmentID = &d
		}
		if dname.Valid {
			n := dname.String
			u.DepartmentName = &n
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser adds a staff or admin account.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (int, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if n := utf8.RuneCountInString(in.Name); n < 2 || n > 80 {
		return 0, apperr.BadRequest(apperr.CodeValidation, "Name must be 2-80 characters")
	}
	if _, err := mail.ParseAddress(in.Email); err != nil || len(in.Email) > 160 {
		return 0, apperr.BadRequest(apperr.CodeValidation, "Please enter a valid email")
	}
	if len(in.Password) < 8 {
		return 0, apperr.BadRequest(apperr.CodeValidation, "Password must be at least 8 characters")
	}
	switch in.Role {
	case RoleAdmin:
		in.DepartmentID = nil
	case RoleStaff:
		if in.DepartmentID == nil {
			return 0, apperr.BadRequest(apperr.CodeValidation, "Staff must belong to a department")
		}
	default:
		return 0, apperr.BadRequest(apperr.CodeValidation, "Role must be admin or staff")
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	var dept any
	if in.DepartmentID != nil {
		dept = *in.DepartmentID
	}
	var id int
	err = s.db.QueryRowContext(ctx, `INSERT INTO staff_users (name, email, password_hash, role, department_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, in.Name, in.Email, hash, in.Role, dept).Scan(&id)
	if pqErr, ok := err.(*pq.Error); ok {
		switch pqErr.Code {
		case "23505":
			return 0, apperr.Conflict(apperr.CodeConflict, "A user with this email already exists")
		case "23503":
			return 0, apperr.BadRequest(apperr.CodeValidation, "Department does not exist")
		}
	}
	return id, err
}

// SetUserActive enables/disables an account.
func (s *Service) SetUserActive(ctx context.Context, actor Actor, id int, active bool) error {
	if id == actor.ID && !active {
		return apperr.BadRequest(apperr.CodeValidation, "You cannot deactivate your own account")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE staff_users SET is_active = $2 WHERE id = $1`, id, active)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFound("user_not_found", "User not found")
	}
	return nil
}
