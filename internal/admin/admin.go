// Package admin serves the web UI and its JSON API: server settings and
// management of the files in the media folder.
package admin

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// Setting is one configuration value as shown in the UI. All values are
// strings in flag syntax; lists are comma separated.
type Setting struct {
	Name      string   `json:"name"`
	Env       string   `json:"env"`
	Usage     string   `json:"usage"`
	Type      string   `json:"type"` // string, int, bool, enum or list
	Options   []string `json:"options,omitempty"`
	Value     string   `json:"value"`               // value of the running server
	Default   string   `json:"default"`             // value without the config file
	FileValue *string  `json:"fileValue,omitempty"` // value in the config file
	Source    string   `json:"source"`              // flag, env, file or default
	Locked    bool     `json:"locked"`              // set by flag/env, not editable
}

// ConfigStore reads and saves settings. Save takes setting names mapped to
// new values, nil removing the setting from the file. Errors made with Invalid
// or Conflict are reported to the client with their status code.
type ConfigStore interface {
	Settings() (file string, settings []Setting, err error)
	Save(updates map[string]*string) error
}

// HTTPError is an error with the HTTP status to report it with.
type HTTPError struct {
	Status int
	Err    error
}

func (e *HTTPError) Error() string { return e.Err.Error() }
func (e *HTTPError) Unwrap() error { return e.Err }

// Invalid marks err as a client input error (400).
func Invalid(err error) error { return &HTTPError{http.StatusBadRequest, err} }

// Conflict marks err as a conflict with the current state (409).
func Conflict(err error) error { return &HTTPError{http.StatusConflict, err} }

// Server is the admin HTTP server.
type Server struct {
	UIDir   string // built web UI; empty or missing serves only the API
	Config  ConfigStore
	Restart func() // restarts the process to apply saved settings
	Logger  *slog.Logger

	root    *os.Root
	started time.Time

	mu             sync.Mutex
	restartPending bool
}

// New returns a server managing the files under mediaDir.
func New(mediaDir, uiDir string, config ConfigStore, restart func(), logger *slog.Logger) (*Server, error) {
	root, err := os.OpenRoot(mediaDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		UIDir:   uiDir,
		Config:  config,
		Restart: restart,
		Logger:  logger,
		root:    root,
		started: time.Now(),
	}, nil
}

// Close releases the media folder handle.
func (s *Server) Close() error { return s.root.Close() }

// Handler returns the HTTP handler with request logging.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("POST /api/restart", s.restart)
	mux.HandleFunc("GET /api/files", s.listFiles)
	mux.HandleFunc("DELETE /api/files", s.deleteFile)
	mux.HandleFunc("POST /api/files/upload", s.upload)
	mux.HandleFunc("POST /api/files/mkdir", s.mkdir)
	mux.HandleFunc("POST /api/files/move", s.move)
	mux.HandleFunc("GET /api/files/download", s.download)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, &HTTPError{http.StatusNotFound, errors.New("not found")})
	})
	mux.HandleFunc("/", s.serveUI)
	return s.logRequests(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "startedAt": s.started})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	file, settings, err := s.Config.Settings()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.mu.Lock()
	pending := s.restartPending
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"file":           file,
		"restartPending": pending,
		"settings":       settings,
	})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var updates map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		s.fail(w, r, Invalid(err))
		return
	}
	if err := s.Config.Save(updates); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(updates) > 0 {
		s.mu.Lock()
		s.restartPending = true
		s.mu.Unlock()
	}
	s.getConfig(w, r)
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	_ = http.NewResponseController(w).Flush()
	s.Logger.Info("restart requested from the web UI")
	go s.Restart()
}

// serveUI serves the built web UI, falling back to index.html for client
// side routes.
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if s.UIDir == "" {
		http.Error(w, "web UI is not available: the UI directory is not set", http.StatusNotFound)
		return
	}
	ui := os.DirFS(s.UIDir)
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if fi, err := fs.Stat(ui, p); err != nil || fi.IsDir() {
		p = "index.html"
	}
	if strings.HasPrefix(p, "assets/") {
		// Vite puts a content hash in asset file names.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if _, err := fs.Stat(ui, p); err != nil {
		http.Error(w, "web UI is not available: "+s.UIDir+" has no index.html (run npm run build)", http.StatusNotFound)
		return
	}
	http.ServeFileFS(w, r, ui, p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail reports err as a JSON {"error": "..."} response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	var he *HTTPError
	switch {
	case errors.As(err, &he):
		status = he.Status
	case errors.Is(err, fs.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, fs.ErrExist):
		status = http.StatusConflict
	case errors.Is(err, fs.ErrInvalid):
		status = http.StatusBadRequest
	}
	level := slog.LevelWarn
	if status >= 500 {
		level = slog.LevelError
	}
	s.Logger.Log(r.Context(), level, "request failed", "method", r.Method, "url", r.URL.String(), "status", status, "error", err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.Logger.Debug("http",
			"method", r.Method,
			"url", r.URL.String(),
			"status", sw.status,
			"took", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}
