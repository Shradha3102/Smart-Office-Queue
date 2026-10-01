package httpapi

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"smartqueue/internal/apperr"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController / websocket hijacking reach the real writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack is required for WebSocket upgrades.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return h.Hijack()
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/api/health" {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(), "ip", clientIP(r, false))
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "err", v, "stack", string(debug.Stack()))
				writeError(w, apperr.New(http.StatusInternalServerError, "internal_error", "Something went wrong"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func cors(allow func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" && allow(origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Token-Secret")
				h.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// OriginMatcher builds an origin predicate from a comma separated list ("*" = any).
func OriginMatcher(list string) func(string) bool {
	var allowed []string
	any := false
	for _, o := range strings.Split(list, ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o == "*" {
			any = true
		} else if o != "" {
			allowed = append(allowed, o)
		}
	}
	return func(origin string) bool {
		if any {
			return true
		}
		for _, a := range allowed {
			if strings.EqualFold(a, origin) {
				return true
			}
		}
		return false
	}
}

// rateLimiter is a fixed-window per-IP limiter for abuse-prone public endpoints.
type rateLimiter struct {
	mu         sync.Mutex
	limit      int
	window     time.Duration
	hits       map[string]*windowCount
	trustProxy bool
}

type windowCount struct {
	start time.Time
	n     int
}

func newRateLimiter(limit int, window time.Duration, trustProxy bool) *rateLimiter {
	rl := &rateLimiter{limit: limit, window: window, hits: map[string]*windowCount{}, trustProxy: trustProxy}
	go func() {
		for range time.Tick(window) {
			rl.mu.Lock()
			for k, v := range rl.hits {
				if time.Since(v.start) > window {
					delete(rl.hits, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(key string) bool {
	if rl.limit <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	wc, ok := rl.hits[key]
	if !ok || now.Sub(wc.start) > rl.window {
		rl.hits[key] = &windowCount{start: now, n: 1}
		return true
	}
	wc.n++
	return wc.n <= rl.limit
}

func (rl *rateLimiter) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r, rl.trustProxy)) {
			writeError(w, apperr.New(http.StatusTooManyRequests, apperr.CodeRateLimited, "Too many requests, please wait a minute"))
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
