package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"smartqueue/internal/auth"
	"smartqueue/internal/queue"
	"smartqueue/internal/realtime"
	"smartqueue/internal/testutil"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestEndToEndAPI(t *testing.T) {
	f := testutil.New(t)
	allow := OriginMatcher("http://localhost:3000")
	hub := realtime.NewHub(allow)
	loc, _ := time.LoadLocation("Asia/Kolkata")
	q := queue.NewService(f.DB, loc, hub)
	a := auth.NewService(f.DB, strings.Repeat("s", 32), time.Hour)
	srv := httptest.NewServer(NewRouter(f.DB, a, q, hub, Options{AllowOrigin: allow, TokenRatePerMin: 100, LoginRatePerMin: 100}))
	defer srv.Close()
	defer hub.Close()

	// Realtime subscriber.
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	var hello map[string]any
	if err := ws.ReadJSON(&hello); err != nil || hello["type"] != "hello" {
		t.Fatalf("hello: %v %v", hello, err)
	}

	pub := &client{t: t, base: srv.URL}
	var depts []map[string]any
	if code := pub.do("GET", "/api/departments", nil, &depts); code != 200 || len(depts) != 4 {
		t.Fatalf("departments %d %v", code, depts)
	}

	itID := f.Depts["IT"]
	var created struct {
		Token        queue.Token `json:"token"`
		AccessSecret string      `json:"access_secret"`
	}
	if code := pub.do("POST", "/api/tokens", map[string]any{"department_id": itID, "visitor_name": "Neha Shah"}, &created); code != 201 {
		t.Fatalf("create token: %d", code)
	}
	if created.Token.TokenNumber != "IT-001" || created.AccessSecret == "" {
		t.Fatalf("created %+v", created)
	}
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ev queue.Event
	if err := ws.ReadJSON(&ev); err != nil || ev.Action != "created" || ev.DepartmentIDs[0] != itID {
		t.Fatalf("ws event %+v %v", ev, err)
	}

	// Error envelope.
	var apiErr struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if code := pub.do("POST", "/api/tokens", map[string]any{"department_id": itID}, &apiErr); code != 400 || apiErr.Error.Code != "validation_error" {
		t.Fatalf("validation: %d %+v", code, apiErr)
	}
	if code := pub.do("GET", "/api/staff/dashboard", nil, &apiErr); code != 401 {
		t.Fatalf("expected 401, got %d", code)
	}

	// Staff login + flow.
	var login auth.LoginResult
	if code := pub.do("POST", "/api/auth/login", map[string]any{"email": "it@office.local", "password": "wrong"}, nil); code != 401 {
		t.Fatalf("bad password should be 401, got %d", code)
	}
	if code := pub.do("POST", "/api/auth/login", map[string]any{"email": "IT@office.local", "password": "Staff@123"}, &login); code != 200 || login.Token == "" {
		t.Fatalf("login %d", code)
	}
	staff := &client{t: t, base: srv.URL, token: login.Token}

	var called queue.Token
	if code := staff.do("POST", fmt.Sprintf("/api/staff/departments/%d/call-next", itID), nil, &called); code != 200 || called.TokenNumber != "IT-001" {
		t.Fatalf("call next %d %+v", code, called)
	}
	if code := staff.do("POST", fmt.Sprintf("/api/staff/departments/%d/call-next", itID), nil, &apiErr); code != 409 || apiErr.Error.Code != "already_serving" {
		t.Fatalf("second call next %d %+v", code, apiErr)
	}
	if code := staff.do("POST", fmt.Sprintf("/api/staff/departments/%d/call-next", f.Depts["HR"]), nil, nil); code != 403 {
		t.Fatalf("cross-department call should be 403, got %d", code)
	}
	if code := staff.do("POST", "/api/staff/tokens/"+called.ID+"/complete", nil, nil); code != 200 {
		t.Fatalf("complete %d", code)
	}
	if code := staff.do("POST", fmt.Sprintf("/api/staff/departments/%d/pause", itID), map[string]any{"reason": "Tea break"}, nil); code != 200 {
		t.Fatalf("pause %d", code)
	}
	if code := pub.do("POST", "/api/tokens", map[string]any{"department_id": itID, "visitor_name": "Late Comer"}, &apiErr); code != 409 || apiErr.Error.Code != "department_paused" {
		t.Fatalf("paused create %d %+v", code, apiErr)
	}
	var dash queue.Dashboard
	if code := staff.do("GET", "/api/staff/dashboard", nil, &dash); code != 200 || dash.Totals.Completed != 1 || len(dash.Departments) != 1 {
		t.Fatalf("dashboard %d %+v", code, dash)
	}
	// Staff cannot use admin endpoints.
	if code := staff.do("GET", "/api/admin/users", nil, nil); code != 403 {
		t.Fatalf("admin endpoint as staff: %d", code)
	}

	// Visitor cancel needs the secret.
	var c2 struct {
		Token        queue.Token `json:"token"`
		AccessSecret string      `json:"access_secret"`
	}
	staff.do("POST", fmt.Sprintf("/api/staff/departments/%d/resume", itID), nil, nil)
	pub.do("POST", "/api/tokens", map[string]any{"department_id": itID, "visitor_name": "Will Cancel"}, &c2)
	if code := pub.do("POST", "/api/tokens/"+c2.Token.ID+"/cancel", map[string]any{"access_secret": "nope"}, nil); code != 403 {
		t.Fatalf("cancel with bad secret: %d", code)
	}
	var st queue.TokenStatus
	if code := pub.do("POST", "/api/tokens/"+c2.Token.ID+"/cancel", map[string]any{"access_secret": c2.AccessSecret}, &st); code != 200 || st.Token.Status != "cancelled" {
		t.Fatalf("cancel %d %+v", code, st)
	}

	// CORS preflight.
	req, _ := http.NewRequest("OPTIONS", srv.URL+"/api/tokens", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("cors: %v %v", res, err)
	}
	res.Body.Close()
}

func TestPasswordHashing(t *testing.T) {
	h, err := auth.HashPassword("S3cret!pass")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(h, "S3cret!pass") || auth.CheckPassword(h, "S3cret!pasS") || auth.CheckPassword("garbage", "x") {
		t.Fatal("password check failed")
	}
}
