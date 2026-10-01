// Package httpapi exposes the REST + WebSocket API.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smartqueue/internal/apperr"
	"smartqueue/internal/auth"
	"smartqueue/internal/queue"
	"smartqueue/internal/realtime"
)

// Server wires handlers to services.
type Server struct {
	db    *sql.DB
	auth  *auth.Service
	queue *queue.Service
	hub   *realtime.Hub
}

// Options configures the router.
type Options struct {
	AllowOrigin     func(string) bool
	TrustProxy      bool
	TokenRatePerMin int
	LoginRatePerMin int
	// WebDir optionally serves a built Flutter web app (flutter build web) at "/".
	WebDir string
}

// NewRouter builds the HTTP handler.
func NewRouter(db *sql.DB, a *auth.Service, q *queue.Service, hub *realtime.Hub, opt Options) http.Handler {
	s := &Server{db: db, auth: a, queue: q, hub: hub}
	tokenRL := newRateLimiter(opt.TokenRatePerMin, time.Minute, opt.TrustProxy)
	loginRL := newRateLimiter(opt.LoginRatePerMin, time.Minute, opt.TrustProxy)

	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/departments", s.listDepartments)
	mux.HandleFunc("GET /api/departments/{id}", s.getDepartment)
	mux.HandleFunc("POST /api/tokens", tokenRL.wrap(s.createToken))
	mux.HandleFunc("GET /api/tokens/{id}", s.getToken)
	mux.HandleFunc("POST /api/tokens/{id}/cancel", s.cancelToken)
	mux.Handle("GET /ws", hub)
	mux.Handle("GET /api/ws", hub)

	// Auth
	mux.HandleFunc("POST /api/auth/login", loginRL.wrap(s.login))
	mux.HandleFunc("GET /api/auth/me", s.staff(s.me))

	// Staff (department-scoped; admins can act on any department)
	mux.HandleFunc("GET /api/staff/departments/{id}/queue", s.staff(s.deptQueue))
	mux.HandleFunc("POST /api/staff/departments/{id}/call-next", s.staff(s.callNext))
	mux.HandleFunc("POST /api/staff/departments/{id}/pause", s.staff(s.pause))
	mux.HandleFunc("POST /api/staff/departments/{id}/resume", s.staff(s.resume))
	mux.HandleFunc("POST /api/staff/tokens", s.staff(s.staffCreateToken))
	mux.HandleFunc("POST /api/staff/tokens/{id}/complete", s.staff(s.complete))
	mux.HandleFunc("POST /api/staff/tokens/{id}/no-show", s.staff(s.noShow))
	mux.HandleFunc("POST /api/staff/tokens/{id}/transfer", s.staff(s.transfer))
	mux.HandleFunc("POST /api/staff/tokens/{id}/priority", s.staff(s.priority))
	mux.HandleFunc("POST /api/staff/tokens/{id}/cancel", s.staff(s.staffCancel))
	mux.HandleFunc("GET /api/staff/dashboard", s.staff(s.dashboard))

	// Admin
	mux.HandleFunc("GET /api/admin/users", s.admin(s.listUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.updateUser))
	mux.HandleFunc("PATCH /api/admin/departments/{id}", s.admin(s.updateDepartment))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apperr.NotFound("not_found", "Endpoint not found"))
	})
	if opt.WebDir != "" {
		mux.Handle("/", spaHandler(opt.WebDir))
	}

	var h http.Handler = mux
	h = cors(opt.AllowOrigin)(h)
	h = recoverer(h)
	h = logging(h)
	return h
}

// spaHandler serves static files and falls back to index.html for client-side routes.
func spaHandler(dir string) http.Handler {
	fsys := http.Dir(dir)
	files := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, apperr.New(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
			return
		}
		if f, err := fsys.Open(path.Clean(r.URL.Path)); err == nil {
			st, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	if ae, ok := apperr.As(err); ok {
		writeJSON(w, ae.Status, map[string]any{"error": ae})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error("unhandled error", "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": apperr.New(http.StatusInternalServerError, "internal_error", "Something went wrong, please try again")})
}

func decode(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty body is fine for optional payloads
		}
		return apperr.BadRequest(apperr.CodeValidation, "Invalid JSON body: "+err.Error())
	}
	return nil
}

func pathInt(r *http.Request, name string) (int, error) {
	v, err := strconv.Atoi(r.PathValue(name))
	if err != nil || v <= 0 {
		return 0, apperr.BadRequest(apperr.CodeValidation, "Invalid "+name)
	}
	return v, nil
}

type actorHandler func(w http.ResponseWriter, r *http.Request, a auth.Actor)

func (s *Server) staff(h actorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(hdr, "Bearer ")
		if !ok || token == "" {
			writeError(w, apperr.Unauthorized("Please log in"))
			return
		}
		a, err := s.auth.Verify(token)
		if err != nil {
			writeError(w, err)
			return
		}
		active, err := s.auth.IsActive(r.Context(), a.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !active {
			writeError(w, apperr.Unauthorized("Your account is disabled"))
			return
		}
		h(w, r.WithContext(auth.WithActor(r.Context(), a)), a)
	}
}

func (s *Server) admin(h actorHandler) http.HandlerFunc {
	return s.staff(func(w http.ResponseWriter, r *http.Request, a auth.Actor) {
		if !a.IsAdmin() {
			writeError(w, apperr.Forbidden("Administrator access required"))
			return
		}
		h(w, r, a)
	})
}

// ---------------------------------------------------------------------------
// public handlers
// ---------------------------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbOK := s.db.PingContext(ctx) == nil
	status := http.StatusOK
	if !dbOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[dbOK],
		"database": dbOK, "ws_clients": s.hub.Clients(), "time": time.Now().UTC()})
}

func (s *Server) listDepartments(w http.ResponseWriter, r *http.Request) {
	d, err := s.queue.ListDepartments(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) getDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := s.queue.GetDepartment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in queue.CreateTokenInput
	if err := decode(r, &in); err != nil {
		writeError(w, err)
		return
	}
	t, err := s.queue.CreateToken(r.Context(), nil, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getToken(w http.ResponseWriter, r *http.Request) {
	t, err := s.queue.TokenStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) cancelToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccessSecret string `json:"access_secret"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	secret := r.Header.Get("X-Token-Secret")
	if secret == "" {
		secret = body.AccessSecret
	}
	t, err := s.queue.CancelByVisitor(r.Context(), r.PathValue("id"), secret)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ---------------------------------------------------------------------------
// auth handlers
// ---------------------------------------------------------------------------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, err)
		return
	}
	res, err := s.auth.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, a auth.Actor) {
	writeJSON(w, http.StatusOK, a)
}

// ---------------------------------------------------------------------------
// staff handlers
// ---------------------------------------------------------------------------

func (s *Server) deptQueue(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := s.queue.DepartmentQueue(r.Context(), a, id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) callNext(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	t, err := s.queue.CallNext(r.Context(), a, id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	s.setPaused(w, r, a, true)
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	s.setPaused(w, r, a, false)
}

func (s *Server) setPaused(w http.ResponseWriter, r *http.Request, a auth.Actor, paused bool) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.queue.SetPaused(r.Context(), a, id, paused, body.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) staffCreateToken(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	var in queue.CreateTokenInput
	if err := decode(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if !a.CanManage(in.DepartmentID) {
		writeError(w, apperr.Forbidden("You can only issue tokens for your own department"))
		return
	}
	t, err := s.queue.CreateToken(r.Context(), &a, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	t, err := s.queue.Complete(r.Context(), a, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) noShow(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	res, err := s.queue.NoShow(r.Context(), a, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	var body struct {
		DepartmentID int    `json:"department_id"`
		Note         string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	t, err := s.queue.Transfer(r.Context(), a, r.PathValue("id"), body.DepartmentID, body.Note)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) priority(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	var body struct {
		IsPriority bool   `json:"is_priority"`
		Reason     string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	t, err := s.queue.SetPriority(r.Context(), a, r.PathValue("id"), body.IsPriority, body.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) staffCancel(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	t, err := s.queue.CancelByStaff(r.Context(), a, r.PathValue("id"), body.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	dept := 0
	if v := r.URL.Query().Get("department_id"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, apperr.BadRequest(apperr.CodeValidation, "Invalid department_id"))
			return
		}
		dept = d
	}
	d, err := s.queue.Dashboard(r.Context(), a, dept)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ---------------------------------------------------------------------------
// admin handlers
// ---------------------------------------------------------------------------

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ auth.Actor) {
	u, err := s.auth.ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, _ auth.Actor) {
	var in auth.CreateUserInput
	if err := decode(r, &in); err != nil {
		writeError(w, err)
		return
	}
	id, err := s.auth.CreateUser(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"id": id})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		IsActive *bool `json:"is_active"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if body.IsActive == nil {
		writeError(w, apperr.BadRequest(apperr.CodeValidation, "is_active is required"))
		return
	}
	if err := s.auth.SetUserActive(r.Context(), a, id, *body.IsActive); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "is_active": *body.IsActive})
}

func (s *Server) updateDepartment(w http.ResponseWriter, r *http.Request, a auth.Actor) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var in queue.UpdateDepartmentInput
	if err := decode(r, &in); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.queue.UpdateDepartment(r.Context(), a, id, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
