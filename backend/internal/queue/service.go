package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"smartqueue/internal/apperr"
	"smartqueue/internal/auth"
)

// Service implements every queue operation. All mutations for a department are
// serialised by a row lock on that department, so concurrent "Call Next" clicks,
// token generation and transfers can never corrupt ordering or numbering.
type Service struct {
	db       *sql.DB
	loc      *time.Location
	notifier Notifier
	now      func() time.Time
}

// NewService builds a queue Service.
func NewService(db *sql.DB, loc *time.Location, n Notifier) *Service {
	if n == nil {
		n = NopNotifier{}
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Service{db: db, loc: loc, notifier: n, now: time.Now}
}

// Location returns the business timezone.
func (s *Service) Location() *time.Location { return s.loc }

func (s *Service) today() string { return s.now().In(s.loc).Format("2006-01-02") }

func (s *Service) dayBounds() (time.Time, time.Time) {
	n := s.now().In(s.loc)
	start := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
	return start, start.AddDate(0, 0, 1)
}

func (s *Service) publish(action, tokenID string, deptIDs ...int) {
	s.notifier.Publish(Event{Type: "queue.updated", DepartmentIDs: deptIDs, TokenID: tokenID, Action: action})
}

// inTx runs fn inside a transaction.
func (s *Service) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// withTokenTx locks (in ascending id order, to avoid deadlocks) the token's department
// plus extraDept (if > 0), then the token row itself, and runs fn.
func (s *Service) withTokenTx(ctx context.Context, tokenID string, extraDept int,
	fn func(tx *sql.Tx, t *Token, depts map[int]Department) error) error {
	if !uuidRe.MatchString(tokenID) {
		return errTokenNotFound()
	}
	for attempt := 0; attempt < 3; attempt++ {
		var deptID int
		err := s.db.QueryRowContext(ctx, `SELECT department_id FROM tokens WHERE id = $1`, tokenID).Scan(&deptID)
		if errors.Is(err, sql.ErrNoRows) {
			return errTokenNotFound()
		}
		if err != nil {
			return err
		}
		retry := false
		err = s.inTx(ctx, func(tx *sql.Tx) error {
			ids := []int{deptID}
			if extraDept > 0 && extraDept != deptID {
				ids = append(ids, extraDept)
			}
			sort.Ints(ids)
			depts := make(map[int]Department, len(ids))
			for _, id := range ids {
				d, err := getDepartment(ctx, tx, id, true)
				if err != nil {
					return err
				}
				depts[id] = d
			}
			t, err := getToken(ctx, tx, tokenID, true)
			if err != nil {
				return err
			}
			if t.DepartmentID != deptID { // moved by a concurrent transfer; take the new locks
				retry = true
				return errRetry
			}
			return fn(tx, &t, depts)
		})
		if retry {
			continue
		}
		return err
	}
	return apperr.Conflict(apperr.CodeConflict, "The token is being modified, please retry")
}

var errRetry = errors.New("retry")

// ---------------------------------------------------------------------------
// Departments
// ---------------------------------------------------------------------------

// ListDepartments returns every department with its live queue summary.
func (s *Service) ListDepartments(ctx context.Context) ([]DepartmentSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deptCols+` FROM departments ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	var depts []Department
	for rows.Next() {
		d, err := scanDept(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		depts = append(depts, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.summaries(ctx, s.db, depts)
}

// GetDepartment returns one department summary.
func (s *Service) GetDepartment(ctx context.Context, id int) (DepartmentSummary, error) {
	d, err := getDepartment(ctx, s.db, id, false)
	if err != nil {
		return DepartmentSummary{}, err
	}
	out, err := s.summaries(ctx, s.db, []Department{d})
	if err != nil {
		return DepartmentSummary{}, err
	}
	return out[0], nil
}

func (s *Service) summaries(ctx context.Context, q querier, depts []Department) ([]DepartmentSummary, error) {
	defaults := make(map[int]int, len(depts))
	for _, d := range depts {
		defaults[d.ID] = d.AvgServiceMinutes
	}
	svc, err := serviceTimes(ctx, q, defaults)
	if err != nil {
		return nil, err
	}

	type live struct {
		waiting  int
		serving  *string
		calledAt *time.Time
	}
	lv := map[int]*live{}
	rows, err := q.QueryContext(ctx, `
		SELECT department_id,
		       COUNT(*) FILTER (WHERE status = 'waiting'),
		       MAX(token_number) FILTER (WHERE status = 'serving'),
		       MAX(called_at) FILTER (WHERE status = 'serving')
		FROM tokens WHERE status IN ('waiting', 'serving') GROUP BY department_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id      int
			w       int
			serving sql.NullString
			called  sql.NullTime
		)
		if err := rows.Scan(&id, &w, &serving, &called); err != nil {
			return nil, err
		}
		l := &live{waiting: w, calledAt: nullTime(called)}
		if serving.Valid {
			v := serving.String
			l.serving = &v
		}
		lv[id] = l
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]DepartmentSummary, 0, len(depts))
	for _, d := range depts {
		sum := DepartmentSummary{Department: d, EffectiveServiceMinutes: roundTo(svc[d.ID].Minutes(), 1)}
		var servingFor *time.Duration
		if l := lv[d.ID]; l != nil {
			sum.WaitingCount = l.waiting
			sum.NowServing = l.serving
			if l.calledAt != nil {
				el := s.now().Sub(*l.calledAt)
				servingFor = &el
			}
		}
		sum.EstimatedWaitMinutes = estimateWait(sum.WaitingCount, svc[d.ID], servingFor)
		out = append(out, sum)
	}
	return out, nil
}

// SetPaused pauses or resumes a department. Paused departments refuse new tokens,
// incoming transfers and "Call Next"; the current visitor can still be completed.
func (s *Service) SetPaused(ctx context.Context, actor auth.Actor, deptID int, paused bool, reason string) (DepartmentSummary, error) {
	if !actor.CanManage(deptID) {
		return DepartmentSummary{}, apperr.Forbidden("You can only manage your own department")
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 200 {
		return DepartmentSummary{}, apperr.BadRequest(apperr.CodeValidation, "Reason must be at most 200 characters")
	}
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		d, err := getDepartment(ctx, tx, deptID, true)
		if err != nil {
			return err
		}
		if d.IsPaused == paused {
			return nil // idempotent
		}
		changed = true
		if paused {
			_, err = tx.ExecContext(ctx, `UPDATE departments SET is_paused = TRUE, pause_reason = $2, paused_at = now(), updated_at = now() WHERE id = $1`, deptID, reason)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE departments SET is_paused = FALSE, pause_reason = '', paused_at = NULL, updated_at = now() WHERE id = $1`, deptID)
		}
		if err != nil {
			return err
		}
		ev := EvResumed
		if paused {
			ev = EvPaused
		}
		return addEvent(ctx, tx, "", deptID, ev, &actor.ID, map[string]any{"reason": reason})
	})
	if err != nil {
		return DepartmentSummary{}, err
	}
	if changed {
		action := "resumed"
		if paused {
			action = "paused"
		}
		s.publish(action, "", deptID)
	}
	return s.GetDepartment(ctx, deptID)
}

// UpdateDepartmentInput holds admin-editable department fields (nil = unchanged).
type UpdateDepartmentInput struct {
	Name              *string `json:"name"`
	Description       *string `json:"description"`
	AvgServiceMinutes *int    `json:"avg_service_minutes"`
}

// UpdateDepartment edits department settings (admin only).
func (s *Service) UpdateDepartment(ctx context.Context, actor auth.Actor, deptID int, in UpdateDepartmentInput) (DepartmentSummary, error) {
	if !actor.IsAdmin() {
		return DepartmentSummary{}, apperr.Forbidden("Only administrators can edit departments")
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		d, err := getDepartment(ctx, tx, deptID, true)
		if err != nil {
			return err
		}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" || utf8.RuneCountInString(n) > 80 {
				return apperr.BadRequest(apperr.CodeValidation, "Name must be 1-80 characters")
			}
			d.Name = n
		}
		if in.Description != nil {
			d.Description = strings.TrimSpace(*in.Description)
		}
		if in.AvgServiceMinutes != nil {
			if *in.AvgServiceMinutes < 1 || *in.AvgServiceMinutes > 240 {
				return apperr.BadRequest(apperr.CodeValidation, "Average service time must be 1-240 minutes")
			}
			d.AvgServiceMinutes = *in.AvgServiceMinutes
		}
		_, err = tx.ExecContext(ctx, `UPDATE departments SET name=$2, description=$3, avg_service_minutes=$4, updated_at=now() WHERE id=$1`,
			deptID, d.Name, d.Description, d.AvgServiceMinutes)
		return err
	})
	if err != nil {
		return DepartmentSummary{}, err
	}
	s.publish("department_updated", "", deptID)
	return s.GetDepartment(ctx, deptID)
}

// ---------------------------------------------------------------------------
// Token generation & visitor operations
// ---------------------------------------------------------------------------

// CreateTokenInput is a request for a new token.
type CreateTokenInput struct {
	DepartmentID   int    `json:"department_id"`
	VisitorName    string `json:"visitor_name"`
	VisitorPhone   string `json:"visitor_phone"`
	IsPriority     bool   `json:"is_priority"`
	PriorityReason string `json:"priority_reason"`
}

var phoneClean = regexp.MustCompile(`[\s\-()]`)
var phoneValid = regexp.MustCompile(`^\+?[0-9]{7,15}$`)

func (in *CreateTokenInput) normalize() error {
	in.VisitorName = strings.Join(strings.Fields(in.VisitorName), " ")
	n := utf8.RuneCountInString(in.VisitorName)
	if n < 2 || n > 80 {
		return apperr.BadRequest(apperr.CodeValidation, "Please enter your name (2-80 characters)")
	}
	in.VisitorPhone = phoneClean.ReplaceAllString(strings.TrimSpace(in.VisitorPhone), "")
	if in.VisitorPhone != "" && !phoneValid.MatchString(in.VisitorPhone) {
		return apperr.BadRequest(apperr.CodeValidation, "Please enter a valid phone number")
	}
	in.PriorityReason = strings.TrimSpace(in.PriorityReason)
	if in.IsPriority && in.PriorityReason == "" {
		return apperr.BadRequest(apperr.CodeValidation, "A reason is required for a priority token")
	}
	if !in.IsPriority {
		in.PriorityReason = ""
	}
	if utf8.RuneCountInString(in.PriorityReason) > 100 {
		return apperr.BadRequest(apperr.CodeValidation, "Priority reason must be at most 100 characters")
	}
	if in.DepartmentID <= 0 {
		return apperr.BadRequest(apperr.CodeValidation, "Please select a service")
	}
	return nil
}

// CreateToken issues a new token. actor is nil for self-service visitors.
func (s *Service) CreateToken(ctx context.Context, actor *auth.Actor, in CreateTokenInput) (*CreatedToken, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	secret, err := auth.NewSecret()
	if err != nil {
		return nil, err
	}
	source := "visitor"
	var actorID *int
	if actor != nil {
		source = "staff"
		actorID = &actor.ID
	}
	var id string
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		d, err := getDepartment(ctx, tx, in.DepartmentID, true)
		if err != nil {
			return err
		}
		if d.IsPaused {
			msg := d.Name + " is currently paused and not accepting new tokens"
			if d.PauseReason != "" {
				msg += " (" + d.PauseReason + ")"
			}
			return apperr.Conflict(apperr.CodeDepartmentPaused, msg)
		}
		if in.VisitorPhone != "" {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT token_number FROM tokens
				WHERE department_id = $1 AND visitor_phone = $2 AND status IN ('waiting','serving') LIMIT 1`,
				d.ID, in.VisitorPhone).Scan(&existing)
			if err == nil {
				return apperr.Conflict(apperr.CodeDuplicateToken,
					fmt.Sprintf("This phone number already has an active token (%s) for %s", existing, d.Name))
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		day := s.today()
		seq, err := nextSeq(ctx, tx, d.ID, day)
		if err != nil {
			return err
		}
		number := fmt.Sprintf("%s-%03d", d.Code, seq)
		lane := 0
		if in.IsPriority {
			lane = 1
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO tokens
			(token_number, department_id, business_date, seq, visitor_name, visitor_phone, is_priority,
			 priority_reason, priority_lane, queue_seq, status, access_secret_hash, source)
			VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9, nextval('token_queue_seq'), 'waiting', $10, $11)
			RETURNING id`,
			number, d.ID, day, seq, in.VisitorName, in.VisitorPhone, in.IsPriority, in.PriorityReason,
			lane, auth.HashSecret(secret), source).Scan(&id)
		if err != nil {
			return err
		}
		return addEvent(ctx, tx, id, d.ID, EvCreated, actorID, map[string]any{
			"token_number": number, "priority": in.IsPriority, "source": source})
	})
	if err != nil {
		return nil, err
	}
	s.publish("created", id, in.DepartmentID)
	st, err := s.TokenStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	return &CreatedToken{TokenStatus: *st, AccessSecret: secret}, nil
}

// TokenStatus returns a token with its live position and ETA (public view, phone hidden).
func (s *Service) TokenStatus(ctx context.Context, id string) (*TokenStatus, error) {
	t, err := getToken(ctx, s.db, id, false)
	if err != nil {
		return nil, err
	}
	d, err := getDepartment(ctx, s.db, t.DepartmentID, false)
	if err != nil {
		return nil, err
	}
	serving, err := servingToken(ctx, s.db, d.ID)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusWaiting {
		var ahead int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens
			WHERE department_id = $1 AND status = 'waiting'
			  AND (priority_lane > $2 OR (priority_lane = $2 AND queue_seq < $3))`,
			t.DepartmentID, t.lane, t.queueSeq).Scan(&ahead); err != nil {
			return nil, err
		}
		svc, err := serviceTimes(ctx, s.db, map[int]int{d.ID: d.AvgServiceMinutes})
		if err != nil {
			return nil, err
		}
		s.fillLive(&t, ahead, svc[d.ID], serving)
	}
	st := &TokenStatus{
		Token:      t,
		Department: DepartmentInfo{ID: d.ID, Code: d.Code, Name: d.Name, IsPaused: d.IsPaused, PauseReason: d.PauseReason},
	}
	st.Token.VisitorPhone = maskPhone(t.VisitorPhone)
	if serving != nil {
		n := serving.TokenNumber
		st.NowServing = &n
	}
	return st, nil
}

func (s *Service) fillLive(t *Token, ahead int, avg time.Duration, serving *Token) {
	var servingFor *time.Duration
	if serving != nil && serving.CalledAt != nil {
		el := s.now().Sub(*serving.CalledAt)
		servingFor = &el
	}
	pos := ahead + 1
	eta := estimateWait(ahead, avg, servingFor)
	t.Position, t.PeopleAhead, t.EstimatedWaitMinutes = &pos, &ahead, &eta
}

func maskPhone(p string) string {
	if len(p) <= 4 {
		return p
	}
	return strings.Repeat("•", len(p)-4) + p[len(p)-4:]
}

// CancelByVisitor cancels a token using the secret handed out at creation.
func (s *Service) CancelByVisitor(ctx context.Context, id, secret string) (*TokenStatus, error) {
	if secret == "" {
		return nil, apperr.Forbidden("Missing token access key")
	}
	var deptID int
	err := s.withTokenTx(ctx, id, 0, func(tx *sql.Tx, t *Token, _ map[int]Department) error {
		if !secretMatches(t.secret, secret) {
			return apperr.Forbidden("You are not allowed to cancel this token")
		}
		deptID = t.DepartmentID
		return s.cancelLocked(ctx, tx, t, nil, "Cancelled by visitor")
	})
	if err != nil {
		return nil, err
	}
	s.publish("cancelled", id, deptID)
	return s.TokenStatus(ctx, id)
}

// CancelByStaff cancels a token from the staff console.
func (s *Service) CancelByStaff(ctx context.Context, actor auth.Actor, id, reason string) (*Token, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Cancelled by staff"
	}
	var out Token
	err := s.withTokenTx(ctx, id, 0, func(tx *sql.Tx, t *Token, _ map[int]Department) error {
		if !actor.CanManage(t.DepartmentID) {
			return apperr.Forbidden("You can only manage tokens of your own department")
		}
		if err := s.cancelLocked(ctx, tx, t, &actor.ID, reason); err != nil {
			return err
		}
		out = *t
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.publish("cancelled", id, out.DepartmentID)
	return s.reload(ctx, out.ID)
}

func (s *Service) cancelLocked(ctx context.Context, tx *sql.Tx, t *Token, actorID *int, reason string) error {
	if !t.IsActive() {
		return apperr.Conflict(apperr.CodeInvalidState, "Token "+t.TokenNumber+" is already "+humanStatus(t.Status))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET status = 'cancelled', close_reason = $2,
		closed_at = now(), updated_at = now() WHERE id = $1`, t.ID, reason); err != nil {
		return err
	}
	if err := addEvent(ctx, tx, t.ID, t.DepartmentID, EvCancelled, actorID, map[string]any{
		"reason": reason, "previous_status": t.Status}); err != nil {
		return err
	}
	return nil
}

func humanStatus(st string) string {
	switch st {
	case StatusNoShow:
		return "cancelled after repeated no-shows"
	default:
		return st
	}
}

func secretMatches(hash, secret string) bool {
	return len(hash) == 64 && auth.HashSecret(secret) == hash
}

func (s *Service) reload(ctx context.Context, id string) (*Token, error) {
	t, err := getToken(ctx, s.db, id, false)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------------------------------------------------------------------------
// Staff operations
// ---------------------------------------------------------------------------

// CallNext moves the head of the queue to the counter. Priority tokens are picked
// first, but a visitor already at the counter is never interrupted: the caller must
// complete / no-show them before calling the next token.
func (s *Service) CallNext(ctx context.Context, actor auth.Actor, deptID int) (*Token, error) {
	if !actor.CanManage(deptID) {
		return nil, apperr.Forbidden("You can only manage your own department")
	}
	var id string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		d, err := getDepartment(ctx, tx, deptID, true)
		if err != nil {
			return err
		}
		if d.IsPaused {
			return apperr.Conflict(apperr.CodeDepartmentPaused, d.Name+" is paused. Resume it to call the next token")
		}
		cur, err := servingToken(ctx, tx, deptID)
		if err != nil {
			return err
		}
		if cur != nil {
			return apperr.Conflict(apperr.CodeAlreadyServing,
				"Token "+cur.TokenNumber+" is still being served. Complete it or mark it as no-show first")
		}
		next, err := scanToken(tx.QueryRowContext(ctx, `SELECT `+tokenCols+tokenFrom+`
			WHERE t.department_id = $1 AND t.status = 'waiting'
			ORDER BY t.priority_lane DESC, t.queue_seq ASC
			LIMIT 1 FOR UPDATE OF t`, deptID))
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.Conflict(apperr.CodeQueueEmpty, "No tokens are waiting in "+d.Name)
		}
		if err != nil {
			return err
		}
		var waitSeconds float64
		if err := tx.QueryRowContext(ctx, `UPDATE tokens SET status = 'serving', called_at = now(),
			first_called_at = COALESCE(first_called_at, now()), served_by = $2, updated_at = now()
			WHERE id = $1 RETURNING EXTRACT(EPOCH FROM (now() - enqueued_at))::float8`,
			next.ID, actor.ID).Scan(&waitSeconds); err != nil {
			return err
		}
		id = next.ID
		return addEvent(ctx, tx, next.ID, deptID, EvCalled, &actor.ID, map[string]any{
			"token_number": next.TokenNumber, "wait_seconds": roundTo(waitSeconds, 1),
			"priority": next.InPriorityLane})
	})
	if err != nil {
		return nil, err
	}
	s.publish("called", id, deptID)
	return s.reload(ctx, id)
}

// Complete marks the token at the counter as served.
func (s *Service) Complete(ctx context.Context, actor auth.Actor, id string) (*Token, error) {
	err := s.withTokenTx(ctx, id, 0, func(tx *sql.Tx, t *Token, _ map[int]Department) error {
		if !actor.CanManage(t.DepartmentID) {
			return apperr.Forbidden("You can only manage tokens of your own department")
		}
		if t.Status != StatusServing {
			return apperr.Conflict(apperr.CodeInvalidState, "Only the token currently being served can be completed")
		}
		var serviceSeconds float64
		if err := tx.QueryRowContext(ctx, `UPDATE tokens SET status = 'completed', completed_at = now(),
			closed_at = now(), updated_at = now() WHERE id = $1
			RETURNING EXTRACT(EPOCH FROM (now() - called_at))::float8`, t.ID).Scan(&serviceSeconds); err != nil {
			return err
		}
		return addEvent(ctx, tx, t.ID, t.DepartmentID, EvCompleted, &actor.ID, map[string]any{
			"token_number": t.TokenNumber, "service_seconds": roundTo(serviceSeconds, 1)})
	})
	if err != nil {
		return nil, err
	}
	tok, err := s.reload(ctx, id)
	if err == nil {
		s.publish("completed", id, tok.DepartmentID)
	}
	return tok, err
}

// NoShow handles a called visitor who did not turn up.
// 1st no-show: the token goes to the very end of the queue (it also leaves the priority lane).
// 2nd no-show: the token is cancelled automatically.
func (s *Service) NoShow(ctx context.Context, actor auth.Actor, id string) (*NoShowResult, error) {
	action := ""
	err := s.withTokenTx(ctx, id, 0, func(tx *sql.Tx, t *Token, _ map[int]Department) error {
		if !actor.CanManage(t.DepartmentID) {
			return apperr.Forbidden("You can only manage tokens of your own department")
		}
		if t.Status != StatusServing {
			return apperr.Conflict(apperr.CodeInvalidState, "Only a token that has been called can be marked as no-show")
		}
		count := t.NoShowCount + 1
		if count >= MaxNoShows {
			action = "cancelled"
			if _, err := tx.ExecContext(ctx, `UPDATE tokens SET status = 'no_show', no_show_count = $2,
				close_reason = 'Automatically cancelled after second no-show', closed_at = now(), updated_at = now()
				WHERE id = $1`, t.ID, count); err != nil {
				return err
			}
			return addEvent(ctx, tx, t.ID, t.DepartmentID, EvNoShowCancelled, &actor.ID, map[string]any{
				"token_number": t.TokenNumber, "no_show_count": count})
		}
		action = "requeued"
		if _, err := tx.ExecContext(ctx, `UPDATE tokens SET status = 'waiting', no_show_count = $2,
			priority_lane = 0, queue_seq = nextval('token_queue_seq'), enqueued_at = now(),
			called_at = NULL, served_by = NULL, updated_at = now() WHERE id = $1`, t.ID, count); err != nil {
			return err
		}
		return addEvent(ctx, tx, t.ID, t.DepartmentID, EvNoShowRequeued, &actor.ID, map[string]any{
			"token_number": t.TokenNumber, "no_show_count": count})
	})
	if err != nil {
		return nil, err
	}
	tok, err := s.reload(ctx, id)
	if err != nil {
		return nil, err
	}
	s.publish("no_show_"+action, id, tok.DepartmentID)
	return &NoShowResult{Token: *tok, Action: action}, nil
}

// Transfer moves a waiting or serving token to another department. It receives a new
// number in the target department (e.g. IT-004 -> HR-012), keeps its priority status
// and joins the end of its lane there.
func (s *Service) Transfer(ctx context.Context, actor auth.Actor, id string, targetDeptID int, note string) (*Token, error) {
	if targetDeptID <= 0 {
		return nil, apperr.BadRequest(apperr.CodeValidation, "Please choose a target department")
	}
	note = strings.TrimSpace(note)
	var sourceDept int
	err := s.withTokenTx(ctx, id, targetDeptID, func(tx *sql.Tx, t *Token, depts map[int]Department) error {
		if !actor.CanManage(t.DepartmentID) {
			return apperr.Forbidden("You can only transfer tokens of your own department")
		}
		if !t.IsActive() {
			return apperr.Conflict(apperr.CodeInvalidState, "Only waiting or serving tokens can be transferred")
		}
		if t.DepartmentID == targetDeptID {
			return apperr.BadRequest(apperr.CodeSameDepartment, "The token is already in this department")
		}
		target := depts[targetDeptID]
		if target.IsPaused {
			return apperr.Conflict(apperr.CodeDepartmentPaused, target.Name+" is paused and cannot accept transfers")
		}
		sourceDept = t.DepartmentID
		day := s.today()
		seq, err := nextSeq(ctx, tx, target.ID, day)
		if err != nil {
			return err
		}
		number := fmt.Sprintf("%s-%03d", target.Code, seq)
		lane := 0
		if t.IsPriority {
			lane = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tokens SET department_id = $2, token_number = $3,
			business_date = $4::date, seq = $5, transferred_from = $6, transfer_count = transfer_count + 1,
			status = 'waiting', priority_lane = $7, queue_seq = nextval('token_queue_seq'), no_show_count = 0,
			enqueued_at = now(), called_at = NULL, served_by = NULL, updated_at = now()
			WHERE id = $1`, t.ID, target.ID, number, day, seq, t.TokenNumber, lane); err != nil {
			return err
		}
		details := map[string]any{"from": t.TokenNumber, "to": number, "note": note,
			"from_department_id": t.DepartmentID, "to_department_id": target.ID, "previous_status": t.Status}
		if err := addEvent(ctx, tx, t.ID, t.DepartmentID, EvTransferredOut, &actor.ID, details); err != nil {
			return err
		}
		return addEvent(ctx, tx, t.ID, target.ID, EvTransferredIn, &actor.ID, details)
	})
	if err != nil {
		return nil, err
	}
	s.publish("transferred", id, sourceDept, targetDeptID)
	return s.reload(ctx, id)
}

// SetPriority grants or revokes priority on a waiting token. Order among priority tokens
// stays first-come-first-served (original arrival order).
func (s *Service) SetPriority(ctx context.Context, actor auth.Actor, id string, priority bool, reason string) (*Token, error) {
	reason = strings.TrimSpace(reason)
	if priority && reason == "" {
		return nil, apperr.BadRequest(apperr.CodeValidation, "A reason is required for a priority token")
	}
	if utf8.RuneCountInString(reason) > 100 {
		return nil, apperr.BadRequest(apperr.CodeValidation, "Priority reason must be at most 100 characters")
	}
	if !priority {
		reason = ""
	}
	err := s.withTokenTx(ctx, id, 0, func(tx *sql.Tx, t *Token, _ map[int]Department) error {
		if !actor.CanManage(t.DepartmentID) {
			return apperr.Forbidden("You can only manage tokens of your own department")
		}
		if t.Status != StatusWaiting {
			return apperr.Conflict(apperr.CodeInvalidState, "Priority can only be changed while the token is waiting")
		}
		lane := 0
		if priority {
			lane = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tokens SET is_priority = $2, priority_reason = $3,
			priority_lane = $4, updated_at = now() WHERE id = $1`, t.ID, priority, reason, lane); err != nil {
			return err
		}
		return addEvent(ctx, tx, t.ID, t.DepartmentID, EvPriorityChanged, &actor.ID, map[string]any{
			"priority": priority, "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	tok, err := s.reload(ctx, id)
	if err == nil {
		s.publish("priority_changed", id, tok.DepartmentID)
	}
	return tok, err
}

// DepartmentQueue returns the full live queue for the staff console.
func (s *Service) DepartmentQueue(ctx context.Context, actor auth.Actor, deptID int) (*QueueView, error) {
	if !actor.CanManage(deptID) {
		return nil, apperr.Forbidden("You can only view your own department")
	}
	sum, err := s.GetDepartment(ctx, deptID)
	if err != nil {
		return nil, err
	}
	view := &QueueView{Department: sum, Waiting: []Token{}, Recent: []Token{}}
	if view.Serving, err = servingToken(ctx, s.db, deptID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenCols+tokenFrom+`
		WHERE t.department_id = $1 AND t.status = 'waiting'
		ORDER BY t.priority_lane DESC, t.queue_seq ASC`, deptID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		view.Waiting = append(view.Waiting, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	avg := time.Duration(sum.EffectiveServiceMinutes * float64(time.Minute))
	for i := range view.Waiting {
		s.fillLive(&view.Waiting[i], i, avg, view.Serving)
	}

	start, _ := s.dayBounds()
	rows, err = s.db.QueryContext(ctx, `SELECT `+tokenCols+tokenFrom+`
		WHERE t.department_id = $1 AND t.status IN ('completed','cancelled','no_show') AND t.closed_at >= $2
		ORDER BY t.closed_at DESC LIMIT 15`, deptID, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		view.Recent = append(view.Recent, t)
	}
	return view, rows.Err()
}

// ExpireStale cancels tokens left over from previous business days.
func (s *Service) ExpireStale(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH x AS (
			UPDATE tokens SET status = 'cancelled', close_reason = 'Expired at end of business day',
			       closed_at = now(), updated_at = now()
			WHERE status IN ('waiting','serving') AND business_date < $1::date
			RETURNING id, department_id)
		INSERT INTO token_events (token_id, department_id, event_type)
		SELECT id, department_id, 'expired' FROM x
		RETURNING department_id`, s.today())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	seen := map[int]bool{}
	n := 0
	for rows.Next() {
		var d int
		if err := rows.Scan(&d); err != nil {
			return n, err
		}
		seen[d] = true
		n++
	}
	if n > 0 {
		ids := make([]int, 0, len(seen))
		for d := range seen {
			ids = append(ids, d)
		}
		sort.Ints(ids)
		slog.Info("expired stale tokens", "count", n)
		s.publish("expired", "", ids...)
	}
	return n, rows.Err()
}
