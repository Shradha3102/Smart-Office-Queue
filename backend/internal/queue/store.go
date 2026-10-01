package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"smartqueue/internal/apperr"
)

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const deptCols = `id, code, name, description, avg_service_minutes, is_paused, pause_reason, paused_at, sort_order`

type scanner interface{ Scan(dest ...any) error }

func scanDept(row scanner) (Department, error) {
	var (
		d        Department
		pausedAt sql.NullTime
	)
	err := row.Scan(&d.ID, &d.Code, &d.Name, &d.Description, &d.AvgServiceMinutes, &d.IsPaused, &d.PauseReason, &pausedAt, &d.SortOrder)
	if pausedAt.Valid {
		t := pausedAt.Time
		d.PausedAt = &t
	}
	return d, err
}

const tokenCols = `t.id, t.token_number, t.department_id, d.code, d.name, t.visitor_name, t.visitor_phone,
	t.is_priority, t.priority_reason, t.priority_lane, t.queue_seq, t.status, t.no_show_count,
	t.transfer_count, t.transferred_from, t.source, t.close_reason, t.created_at, t.enqueued_at,
	t.called_at, t.completed_at, t.closed_at, t.access_secret_hash`

const tokenFrom = ` FROM tokens t JOIN departments d ON d.id = t.department_id `

func scanToken(row scanner) (Token, error) {
	var (
		t                          Token
		called, completed, closed  sql.NullTime
		noShows, transfers, lane16 int16
	)
	err := row.Scan(&t.ID, &t.TokenNumber, &t.DepartmentID, &t.DepartmentCode, &t.DepartmentName,
		&t.VisitorName, &t.VisitorPhone, &t.IsPriority, &t.PriorityReason, &lane16, &t.queueSeq,
		&t.Status, &noShows, &transfers, &t.TransferredFrom, &t.Source, &t.CloseReason,
		&t.CreatedAt, &t.EnqueuedAt, &called, &completed, &closed, &t.secret)
	if err != nil {
		return t, err
	}
	t.NoShowCount, t.TransferCount, t.lane = int(noShows), int(transfers), int(lane16)
	t.InPriorityLane = t.lane == 1
	t.CalledAt = nullTime(called)
	t.CompletedAt = nullTime(completed)
	t.ClosedAt = nullTime(closed)
	return t, nil
}

func nullTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func errTokenNotFound() error {
	return apperr.NotFound(apperr.CodeTokenNotFound, "Token not found")
}

func errDeptNotFound() error {
	return apperr.NotFound(apperr.CodeDepartmentNotFound, "Department not found")
}

func getDepartment(ctx context.Context, q querier, id int, forUpdate bool) (Department, error) {
	query := `SELECT ` + deptCols + ` FROM departments WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	d, err := scanDept(q.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, errDeptNotFound()
	}
	return d, err
}

func getToken(ctx context.Context, q querier, id string, forUpdate bool) (Token, error) {
	if !uuidRe.MatchString(id) {
		return Token{}, errTokenNotFound()
	}
	query := `SELECT ` + tokenCols + tokenFrom + ` WHERE t.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF t`
	}
	t, err := scanToken(q.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, errTokenNotFound()
	}
	return t, err
}

func servingToken(ctx context.Context, q querier, deptID int) (*Token, error) {
	t, err := scanToken(q.QueryRowContext(ctx, `SELECT `+tokenCols+tokenFrom+
		` WHERE t.department_id = $1 AND t.status = 'serving'`, deptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func addEvent(ctx context.Context, q querier, tokenID string, deptID int, typ string, actorID *int, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var tok any
	if tokenID != "" {
		tok = tokenID
	}
	var actor any
	if actorID != nil {
		actor = *actorID
	}
	_, err = q.ExecContext(ctx, `INSERT INTO token_events (token_id, department_id, event_type, actor_id, details)
		VALUES ($1, $2, $3, $4, $5)`, tok, deptID, typ, actor, string(b))
	return err
}

// nextSeq reserves the next daily sequence number for a department. Caller must hold the department lock.
func nextSeq(ctx context.Context, q querier, deptID int, businessDate string) (int, error) {
	var seq int
	err := q.QueryRowContext(ctx, `INSERT INTO token_counters (department_id, business_date, last_seq)
		VALUES ($1, $2::date, 1)
		ON CONFLICT (department_id, business_date) DO UPDATE SET last_seq = token_counters.last_seq + 1
		RETURNING last_seq`, deptID, businessDate).Scan(&seq)
	return seq, err
}

// ---------------------------------------------------------------------------
// Waiting-time estimation
// ---------------------------------------------------------------------------

const (
	learnWindow     = 20 // most recent completions used
	learnMinSamples = 3  // below this, the configured default is used
	minService      = 30 * time.Second
	maxService      = 4 * time.Hour
)

// serviceTimes returns the effective average service duration per department.
func serviceTimes(ctx context.Context, q querier, defaults map[int]int) (map[int]time.Duration, error) {
	out := make(map[int]time.Duration, len(defaults))
	for id, mins := range defaults {
		out[id] = time.Duration(mins) * time.Minute
	}
	rows, err := q.QueryContext(ctx, `
		SELECT department_id, AVG(dur), COUNT(*) FROM (
			SELECT department_id,
			       EXTRACT(EPOCH FROM (completed_at - called_at))::float8 AS dur,
			       ROW_NUMBER() OVER (PARTITION BY department_id ORDER BY completed_at DESC) AS rn
			FROM tokens
			WHERE status = 'completed' AND called_at IS NOT NULL
			  AND completed_at > now() - interval '7 days'
		) x WHERE rn <= $1 GROUP BY department_id`, learnWindow)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id  int
			avg float64
			n   int
		)
		if err := rows.Scan(&id, &avg, &n); err != nil {
			return nil, err
		}
		if _, known := out[id]; !known || n < learnMinSamples {
			continue
		}
		d := time.Duration(avg * float64(time.Second))
		out[id] = min(max(d, minService), maxService)
	}
	return out, rows.Err()
}

// estimateWait returns the expected minutes until a visitor with peopleAhead tokens in
// front of them is called. servingFor is how long the current visitor has been at the
// counter (nil when nobody is being served).
func estimateWait(peopleAhead int, avg time.Duration, servingFor *time.Duration) int {
	var total time.Duration
	if servingFor != nil {
		remaining := avg - *servingFor
		// An overrunning service still needs *some* time to wrap up.
		floor := min(avg, time.Minute) / 2
		if remaining < floor {
			remaining = floor
		}
		total += remaining
	}
	total += time.Duration(peopleAhead) * avg
	if total <= 0 {
		return 0
	}
	return int(math.Ceil(total.Minutes()))
}

func roundTo(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
