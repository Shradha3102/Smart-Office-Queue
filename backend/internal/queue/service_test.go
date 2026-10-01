package queue

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"smartqueue/internal/apperr"
	"smartqueue/internal/auth"
	"smartqueue/internal/testutil"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) Publish(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

type env struct {
	t   *testing.T
	f   *testutil.Fixture
	svc *Service
	rec *recorder
	ctx context.Context
}

func setup(t *testing.T) *env {
	f := testutil.New(t)
	loc, _ := time.LoadLocation("Asia/Kolkata")
	rec := &recorder{}
	return &env{t: t, f: f, svc: NewService(f.DB, loc, rec), rec: rec, ctx: context.Background()}
}

func (e *env) issue(code, name string, priority bool) *CreatedToken {
	e.t.Helper()
	in := CreateTokenInput{DepartmentID: e.f.Depts[code], VisitorName: "Visitor " + name, IsPriority: priority}
	if priority {
		in.PriorityReason = "Senior citizen"
	}
	tok, err := e.svc.CreateToken(e.ctx, nil, in)
	if err != nil {
		e.t.Fatalf("create token %s: %v", name, err)
	}
	return tok
}

func (e *env) callNext(code string) *Token {
	e.t.Helper()
	tok, err := e.svc.CallNext(e.ctx, e.f.Staff[code], e.f.Depts[code])
	if err != nil {
		e.t.Fatalf("call next %s: %v", code, err)
	}
	return tok
}

// waitingNumbers returns the waiting queue in order.
func (e *env) waitingNumbers(code string) []string {
	e.t.Helper()
	q, err := e.svc.DepartmentQueue(e.ctx, e.f.Admin, e.f.Depts[code])
	if err != nil {
		e.t.Fatal(err)
	}
	out := []string{}
	for i, w := range q.Waiting {
		if *w.Position != i+1 {
			e.t.Fatalf("position mismatch for %s: got %d want %d", w.TokenNumber, *w.Position, i+1)
		}
		out = append(out, w.TokenNumber)
	}
	return out
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("queue order\n got: %v\nwant: %v", got, want)
	}
}

func TestTokenNumberingPerDepartment(t *testing.T) {
	e := setup(t)
	a := e.issue("IT", "Asha", false)
	b := e.issue("IT", "Bhavin", false)
	c := e.issue("HR", "Chirag", false)
	d := e.issue("ACC", "Dev", false)
	f := e.issue("ADM", "Fatima", false)
	got := []string{a.Token.TokenNumber, b.Token.TokenNumber, c.Token.TokenNumber, d.Token.TokenNumber, f.Token.TokenNumber}
	eq(t, got, []string{"IT-001", "IT-002", "HR-001", "ACC-001", "ADM-001"})
	if a.AccessSecret == "" || a.Token.Status != StatusWaiting || *a.Token.Position != 1 || *b.Token.Position != 2 {
		t.Fatalf("unexpected created token: %+v", a)
	}

	// Numbering restarts on the next business day.
	e.svc.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	n := e.issue("IT", "Tomorrow", false)
	if n.Token.TokenNumber != "IT-001" {
		t.Fatalf("expected numbering to reset daily, got %s", n.Token.TokenNumber)
	}
}

func TestConcurrentTokenGenerationIsGapFreeAndUnique(t *testing.T) {
	e := setup(t)
	const n = 30
	var wg sync.WaitGroup
	nums := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := e.svc.CreateToken(e.ctx, nil, CreateTokenInput{DepartmentID: e.f.Depts["IT"], VisitorName: fmt.Sprintf("V%02d", i)})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			nums <- tok.Token.TokenNumber
		}(i)
	}
	wg.Wait()
	close(nums)
	var got []string
	for s := range nums {
		got = append(got, s)
	}
	sort.Strings(got)
	for i, s := range got {
		if want := fmt.Sprintf("IT-%03d", i+1); s != want {
			t.Fatalf("got %v", got)
		}
	}
}

func TestPriorityJumpsQueueButNeverInterruptsServing(t *testing.T) {
	e := setup(t)
	e.issue("IT", "N1", false)
	e.issue("IT", "N2", false)
	serving := e.callNext("IT")
	if serving.TokenNumber != "IT-001" || serving.Status != StatusServing {
		t.Fatalf("expected IT-001 serving, got %+v", serving)
	}
	e.issue("IT", "N3", false)
	p := e.issue("IT", "P1", true)
	if *p.Token.Position != 1 {
		t.Fatalf("priority token should be first in line, got position %d", *p.Token.Position)
	}
	e.issue("IT", "P2", true)
	eq(t, e.waitingNumbers("IT"), []string{"IT-004", "IT-005", "IT-002", "IT-003"})

	// The visitor at the counter is NOT displaced.
	_, err := e.svc.CallNext(e.ctx, e.f.Staff["IT"], e.f.Depts["IT"])
	wantCode(t, err, apperr.CodeAlreadyServing)
	q, _ := e.svc.DepartmentQueue(e.ctx, e.f.Admin, e.f.Depts["IT"])
	if q.Serving == nil || q.Serving.TokenNumber != "IT-001" {
		t.Fatalf("serving token changed: %+v", q.Serving)
	}

	if _, err := e.svc.Complete(e.ctx, e.f.Staff["IT"], serving.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.callNext("IT").TokenNumber; got != "IT-004" {
		t.Fatalf("expected priority IT-004 next, got %s", got)
	}
}

func TestNoShowRequeuesThenAutoCancels(t *testing.T) {
	e := setup(t)
	a := e.issue("HR", "A", false)
	e.issue("HR", "B", false)
	e.issue("HR", "C", false)
	staff := e.f.Staff["HR"]

	e.callNext("HR") // A
	res, err := e.svc.NoShow(e.ctx, staff, a.Token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "requeued" || res.Token.Status != StatusWaiting || res.Token.NoShowCount != 1 {
		t.Fatalf("first no-show should requeue: %+v", res)
	}
	eq(t, e.waitingNumbers("HR"), []string{"HR-002", "HR-003", "HR-001"})

	// A newcomer after the requeue lands behind A.
	e.issue("HR", "D", false)
	eq(t, e.waitingNumbers("HR"), []string{"HR-002", "HR-003", "HR-001", "HR-004"})

	for _, want := range []string{"HR-002", "HR-003"} {
		tok := e.callNext("HR")
		if tok.TokenNumber != want {
			t.Fatalf("want %s got %s", want, tok.TokenNumber)
		}
		if _, err := e.svc.Complete(e.ctx, staff, tok.ID); err != nil {
			t.Fatal(err)
		}
	}
	if tok := e.callNext("HR"); tok.ID != a.Token.ID {
		t.Fatalf("expected A to be called again, got %s", tok.TokenNumber)
	}
	res, err = e.svc.NoShow(e.ctx, staff, a.Token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "cancelled" || res.Token.Status != StatusNoShow || res.Token.NoShowCount != 2 || res.Token.ClosedAt == nil {
		t.Fatalf("second no-show should auto-cancel: %+v", res)
	}
	eq(t, e.waitingNumbers("HR"), []string{"HR-004"})

	// No-show only applies to a called token.
	d := e.waitingNumbers("HR")
	_ = d
	q, _ := e.svc.DepartmentQueue(e.ctx, e.f.Admin, e.f.Depts["HR"])
	_, err = e.svc.NoShow(e.ctx, staff, q.Waiting[0].ID)
	wantCode(t, err, apperr.CodeInvalidState)
}

func TestPriorityNoShowGoesToVeryEnd(t *testing.T) {
	e := setup(t)
	p := e.issue("IT", "P", true)
	e.issue("IT", "N1", false)
	e.issue("IT", "N2", false)
	if got := e.callNext("IT"); got.ID != p.Token.ID {
		t.Fatalf("expected priority first")
	}
	if _, err := e.svc.NoShow(e.ctx, e.f.Staff["IT"], p.Token.ID); err != nil {
		t.Fatal(err)
	}
	eq(t, e.waitingNumbers("IT"), []string{"IT-002", "IT-003", "IT-001"})
	st, _ := e.svc.TokenStatus(e.ctx, p.Token.ID)
	if !st.Token.IsPriority || st.Token.InPriorityLane {
		t.Fatalf("token should keep its priority flag but leave the priority lane: %+v", st.Token)
	}
}

func TestPausedDepartmentRules(t *testing.T) {
	e := setup(t)
	staff := e.f.Staff["ACC"]
	dept := e.f.Depts["ACC"]
	e.issue("ACC", "Before pause", false)
	it := e.issue("IT", "To transfer", false)

	if _, err := e.svc.SetPaused(e.ctx, staff, dept, true, "Lunch break"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CreateToken(e.ctx, nil, CreateTokenInput{DepartmentID: dept, VisitorName: "Late"})
	wantCode(t, err, apperr.CodeDepartmentPaused)
	_, err = e.svc.CallNext(e.ctx, staff, dept)
	wantCode(t, err, apperr.CodeDepartmentPaused)
	_, err = e.svc.Transfer(e.ctx, e.f.Staff["IT"], it.Token.ID, dept, "")
	wantCode(t, err, apperr.CodeDepartmentPaused)

	// Existing waiting tokens keep their place and still see a status.
	st, err := e.svc.GetDepartment(e.ctx, dept)
	if err != nil || !st.IsPaused || st.PauseReason != "Lunch break" || st.WaitingCount != 1 {
		t.Fatalf("unexpected paused state %+v %v", st, err)
	}

	if _, err := e.svc.SetPaused(e.ctx, staff, dept, false, ""); err != nil {
		t.Fatal(err)
	}
	e.issue("ACC", "After resume", false)
	if got := e.callNext("ACC").TokenNumber; got != "ACC-001" {
		t.Fatalf("got %s", got)
	}
}

func TestTransferKeepsPriorityAndRenumbers(t *testing.T) {
	e := setup(t)
	e.issue("HR", "H1", false)
	e.issue("HR", "H2 priority", true)
	n := e.issue("IT", "Normal", false)
	p := e.issue("IT", "Priority", true)

	// Staff of another department may not transfer it.
	_, err := e.svc.Transfer(e.ctx, e.f.Staff["HR"], n.Token.ID, e.f.Depts["HR"], "")
	if _, ok := apperr.As(err); !ok || err.(*apperr.Error).Status != 403 {
		t.Fatalf("expected forbidden, got %v", err)
	}
	_, err = e.svc.Transfer(e.ctx, e.f.Staff["IT"], n.Token.ID, e.f.Depts["IT"], "")
	wantCode(t, err, apperr.CodeSameDepartment)

	moved, err := e.svc.Transfer(e.ctx, e.f.Staff["IT"], n.Token.ID, e.f.Depts["HR"], "Needs payroll")
	if err != nil {
		t.Fatal(err)
	}
	if moved.TokenNumber != "HR-003" || moved.TransferredFrom != "IT-001" || moved.DepartmentID != e.f.Depts["HR"] {
		t.Fatalf("bad transfer result %+v", moved)
	}
	// A serving token can be transferred too, and priority is kept.
	if got := e.callNext("IT"); got.ID != p.Token.ID {
		t.Fatal("expected priority IT token to be serving")
	}
	if _, err := e.svc.Transfer(e.ctx, e.f.Staff["IT"], p.Token.ID, e.f.Depts["HR"], ""); err != nil {
		t.Fatal(err)
	}
	eq(t, e.waitingNumbers("HR"), []string{"HR-002", "HR-004", "HR-001", "HR-003"})
	eq(t, e.waitingNumbers("IT"), []string{})

	// The visitor tracks the same token id after the transfer.
	st, err := e.svc.TokenStatus(e.ctx, p.Token.ID)
	if err != nil || st.Token.TokenNumber != "HR-004" || *st.Token.Position != 2 || st.Department.Code != "HR" {
		t.Fatalf("tracking after transfer: %+v %v", st, err)
	}
	if e.rec.count() == 0 {
		t.Fatal("expected realtime events to be published")
	}
}

func TestEstimatedWaitUpdatesWithQueue(t *testing.T) {
	e := setup(t)
	// IT default service time is 6 minutes.
	a := e.issue("IT", "A", false)
	b := e.issue("IT", "B", false)
	c := e.issue("IT", "C", false)
	if *a.Token.EstimatedWaitMinutes != 0 || *c.Token.EstimatedWaitMinutes != 12 {
		t.Fatalf("eta a=%d c=%d", *a.Token.EstimatedWaitMinutes, *c.Token.EstimatedWaitMinutes)
	}
	e.callNext("IT") // A at the counter, just started
	st, _ := e.svc.TokenStatus(e.ctx, b.Token.ID)
	if *st.Token.Position != 1 || *st.Token.EstimatedWaitMinutes != 6 {
		t.Fatalf("b after call: pos=%d eta=%d", *st.Token.Position, *st.Token.EstimatedWaitMinutes)
	}
	// Cancelling B moves C up.
	if _, err := e.svc.CancelByVisitor(e.ctx, b.Token.ID, "wrong-secret"); err == nil {
		t.Fatal("expected forbidden with wrong secret")
	}
	if _, err := e.svc.CancelByVisitor(e.ctx, b.Token.ID, b.AccessSecret); err != nil {
		t.Fatal(err)
	}
	st, _ = e.svc.TokenStatus(e.ctx, c.Token.ID)
	if *st.Token.Position != 1 || *st.Token.EstimatedWaitMinutes != 6 {
		t.Fatalf("c after cancel: pos=%d eta=%d", *st.Token.Position, *st.Token.EstimatedWaitMinutes)
	}
	_, err := e.svc.CancelByVisitor(e.ctx, b.Token.ID, b.AccessSecret)
	wantCode(t, err, apperr.CodeInvalidState)

	dept, _ := e.svc.GetDepartment(e.ctx, e.f.Depts["IT"])
	if dept.WaitingCount != 1 || dept.EstimatedWaitMinutes != 12 || dept.NowServing == nil {
		t.Fatalf("dept summary %+v", dept)
	}
}

func TestEstimateWaitMath(t *testing.T) {
	avg := 6 * time.Minute
	el := 2 * time.Minute
	over := 10 * time.Minute
	cases := []struct {
		ahead int
		svc   *time.Duration
		want  int
	}{
		{0, nil, 0}, {2, nil, 12}, {0, &el, 4}, {1, &el, 10}, {0, &over, 1}, {3, &over, 19},
	}
	for _, c := range cases {
		if got := estimateWait(c.ahead, avg, c.svc); got != c.want {
			t.Errorf("estimateWait(%d, %v) = %d want %d", c.ahead, c.svc, got, c.want)
		}
	}
}

func TestOnlyOneConcurrentCallNextWins(t *testing.T) {
	e := setup(t)
	for i := 0; i < 5; i++ {
		e.issue("ADM", fmt.Sprintf("V%d", i), false)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, busy := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.CallNext(e.ctx, e.f.Staff["ADM"], e.f.Depts["ADM"])
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if ae, isApp := apperr.As(err); isApp && ae.Code == apperr.CodeAlreadyServing {
				busy++
			} else {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || busy != 7 {
		t.Fatalf("ok=%d busy=%d", ok, busy)
	}
}

func TestAuthorizationAndValidation(t *testing.T) {
	e := setup(t)
	e.issue("IT", "X", false)
	_, err := e.svc.CallNext(e.ctx, e.f.Staff["HR"], e.f.Depts["IT"])
	if ae, _ := apperr.As(err); ae == nil || ae.Status != 403 {
		t.Fatalf("expected 403, got %v", err)
	}
	if _, err := e.svc.CallNext(e.ctx, e.f.Admin, e.f.Depts["IT"]); err != nil {
		t.Fatalf("admin should manage any department: %v", err)
	}
	_, err = e.svc.CreateToken(e.ctx, nil, CreateTokenInput{DepartmentID: e.f.Depts["IT"], VisitorName: " "})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.CreateToken(e.ctx, nil, CreateTokenInput{DepartmentID: e.f.Depts["IT"], VisitorName: "Ok", IsPriority: true})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.CreateToken(e.ctx, nil, CreateTokenInput{DepartmentID: 9999, VisitorName: "Ok"})
	wantCode(t, err, apperr.CodeDepartmentNotFound)
	_, err = e.svc.TokenStatus(e.ctx, "not-a-uuid")
	wantCode(t, err, apperr.CodeTokenNotFound)

	in := CreateTokenInput{DepartmentID: e.f.Depts["HR"], VisitorName: "Phone", VisitorPhone: "+91 98765-43210"}
	if _, err := e.svc.CreateToken(e.ctx, nil, in); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateToken(e.ctx, nil, in)
	wantCode(t, err, apperr.CodeDuplicateToken)
}

func TestSetPriorityOnWaitingToken(t *testing.T) {
	e := setup(t)
	e.issue("IT", "A", false)
	b := e.issue("IT", "B", false)
	c := e.issue("IT", "C", false)
	if _, err := e.svc.SetPriority(e.ctx, e.f.Staff["IT"], c.Token.ID, true, "Pregnant"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetPriority(e.ctx, e.f.Staff["IT"], b.Token.ID, true, "Wheelchair"); err != nil {
		t.Fatal(err)
	}
	// Among priority tokens, original arrival order is kept.
	eq(t, e.waitingNumbers("IT"), []string{"IT-002", "IT-003", "IT-001"})
	if _, err := e.svc.SetPriority(e.ctx, e.f.Staff["IT"], b.Token.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	eq(t, e.waitingNumbers("IT"), []string{"IT-003", "IT-001", "IT-002"})
}

func TestDashboardFigures(t *testing.T) {
	e := setup(t)
	staff := e.f.Staff["IT"]
	a := e.issue("IT", "A", false)
	b := e.issue("IT", "B", false)
	e.issue("IT", "C", false)
	e.issue("HR", "D", false)

	e.callNext("IT")
	if _, err := e.svc.Complete(e.ctx, staff, a.Token.ID); err != nil {
		t.Fatal(err)
	}
	e.callNext("IT") // B
	if _, err := e.svc.NoShow(e.ctx, staff, b.Token.ID); err != nil {
		t.Fatal(err)
	}
	e.callNext("IT") // C now serving

	d, err := e.svc.Dashboard(e.ctx, e.f.Admin, 0)
	if err != nil {
		t.Fatal(err)
	}
	var it DeptStats
	for _, s := range d.Departments {
		if s.Code == "IT" {
			it = s
		}
	}
	if it.Issued != 3 || it.Completed != 1 || it.NoShows != 1 || it.Waiting != 1 || it.Serving != 1 ||
		it.NowServing == nil || *it.NowServing != "IT-003" || it.Called != 3 {
		t.Fatalf("IT stats %+v", it)
	}
	if d.Totals.Issued != 4 || d.Totals.Waiting != 2 || d.Totals.Completed != 1 {
		t.Fatalf("totals %+v", d.Totals)
	}

	// Staff only see their own department.
	ds, err := e.svc.Dashboard(e.ctx, e.f.Staff["HR"], e.f.Depts["IT"])
	if err != nil || len(ds.Departments) != 1 || ds.Departments[0].Code != "HR" {
		t.Fatalf("staff dashboard scope: %+v %v", ds, err)
	}
}

func TestExpireStaleTokens(t *testing.T) {
	e := setup(t)
	a := e.issue("IT", "Yesterday", false)
	e.svc.now = func() time.Time { return time.Now().Add(26 * time.Hour) }
	n, err := e.svc.ExpireStale(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("expired %d, %v", n, err)
	}
	st, _ := e.svc.TokenStatus(e.ctx, a.Token.ID)
	if st.Token.Status != StatusCancelled {
		t.Fatalf("status %s", st.Token.Status)
	}
}

var _ = auth.RoleAdmin
