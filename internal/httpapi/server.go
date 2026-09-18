// Package httpapi exposes the REST API and serves the built web UI.
package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeck/internal/codex"
	"codeck/internal/config"
	"codeck/internal/scheduler"
	"codeck/internal/store"
	"codeck/internal/usage"
)

// maxBodyBytes bounds request bodies; every payload this API accepts is small.
const maxBodyBytes = 1 << 20

// Server holds the dependencies shared by all handlers.
type Server struct {
	cfg       config.Config
	db        *store.DB
	codex     *codex.Service
	appServer *codex.AppServer
	sched     *scheduler.Scheduler
	log       *slog.Logger
	version   string
	static    fs.FS
	started   time.Time

	accountMu       sync.Mutex
	accountCached   accountSnapshot
	accountCachedAt time.Time

	usageMu       sync.Mutex
	usageCached   usage.Report
	usageCachedAt time.Time
}

// NewServer wires the HTTP layer. static may be nil, in which case the API is
// served without a web UI (handy for tests and for running the API alone).
// appServer may be nil when Codex App Server failed to start.
func NewServer(cfg config.Config, db *store.DB, svc *codex.Service, sched *scheduler.Scheduler,
	appServer *codex.AppServer, log *slog.Logger, version string, static fs.FS) *Server {
	return &Server{
		cfg: cfg, db: db, codex: svc, sched: sched, appServer: appServer,
		log: log, version: version, static: static,
		started: time.Now(),
	}
}

// Handler builds the routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/dashboard", s.handleDashboard)
	mux.HandleFunc("GET /api/account", s.handleAccount)

	mux.HandleFunc("GET /api/profiles", s.handleListProfiles)
	mux.HandleFunc("POST /api/profiles", s.handleCreateProfile)
	mux.HandleFunc("PUT /api/profiles/{id}", s.handleUpdateProfile)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.handleDeleteProfile)
	mux.HandleFunc("GET /api/profiles/{id}/config", s.handleProfileConfig)
	mux.HandleFunc("POST /api/profiles/{id}/test", s.handleTestProfile)
	mux.HandleFunc("POST /api/profiles/{id}/prompt-preview", s.handlePreviewProfilePrompt)

	mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	mux.HandleFunc("POST /api/conversations", s.handleCreateConversation)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("PATCH /api/conversations/{id}", s.handleUpdateConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)

	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("PUT /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.handleRunTask)
	mux.HandleFunc("GET /api/tasks/{id}/runs", s.handleTaskRuns)

	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/history", s.handleHistory)

	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("GET /api/prices", s.handleListPrices)
	mux.HandleFunc("POST /api/prices", s.handleCreatePrice)
	mux.HandleFunc("PUT /api/prices/{id}", s.handleUpdatePrice)
	mux.HandleFunc("DELETE /api/prices/{id}", s.handleDeletePrice)
	mux.HandleFunc("POST /api/prices/restore", s.handleRestorePrices)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: %s %s", r.Method, r.URL.Path)
	})

	mux.HandleFunc("/", s.handleStatic)

	var handler http.Handler = mux
	handler = s.withRequestSafety(handler)
	if s.cfg.HTTPAuthUser != "" && s.cfg.HTTPAuthPassword != "" {
		handler = s.withBasicAuth(handler)
	}
	return s.withLogging(handler)
}

// withBasicAuth protects both the UI and API when credentials are configured.
func (s *Server) withBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || !constantTimeEqual(user, s.cfg.HTTPAuthUser) ||
			!constantTimeEqual(password, s.cfg.HTTPAuthPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Codeck"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// withRequestSafety applies browser-facing checks to mutating JSON API calls.
func (s *Server) withRequestSafety(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && isMutationMethod(r.Method) {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
					writeError(w, http.StatusForbidden, "cross-origin mutation is not allowed")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// withLogging records one line per request and recovers from handler panics so
// a single bad request cannot take the service down.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if p := recover(); p != nil {
				s.log.Error("handler panicked", "method", r.Method, "path", r.URL.Path, "panic", p)
				if !sw.headerWritten() {
					writeError(sw, http.StatusInternalServerError, "internal error")
				}
			}
		}()

		next.ServeHTTP(sw, r)

		// Streaming responses report their own duration in the transcript, so
		// they are logged at debug level to keep the log readable.
		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/api/chat/stream") {
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "duration", time.Since(started).Round(time.Millisecond))
	})
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer so SSE streaming still works.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) headerWritten() bool { return r.wroteHeader }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so the only thing left to do is note
		// it; the client will see a truncated body.
		slog.Default().Debug("failed to encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// decodeJSON reads a JSON request body into dst, rejecting unknown fields and
// oversized payloads.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "a JSON body is required")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return false
	}
	return true
}

// pathID extracts an integer path parameter such as {id}.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid %s %q", name, raw)
		return 0, false
	}
	return id, true
}

// writeStoreError maps storage errors onto HTTP status codes.
func writeStoreError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "%s not found", what)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "%v", err)
		return
	}
	writeError(w, http.StatusInternalServerError, "%s: %v", what, err)
}

// queryLimit reads a ?limit= parameter, clamped to a sane range.
func queryLimit(r *http.Request, def, max int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
