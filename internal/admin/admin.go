// Package admin serves the web UI and its JSON API: server settings and
// management of the files in the media folder. The API is described by an
// OpenAPI document (see OpenAPI) from which the UI's client is generated.
package admin

import (
	"context"
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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Setting is one configuration value as shown in the UI. All values are
// strings in flag syntax; lists are comma separated.
type Setting struct {
	Name      string   `json:"name"`
	Env       string   `json:"env"`
	Usage     string   `json:"usage"`
	Type      string   `json:"type" enum:"string,int,bool,enum,list"`
	Options   []string `json:"options,omitempty" nullable:"false" doc:"Allowed values of an enum setting."`
	Value     string   `json:"value" doc:"Value of the running server."`
	Default   string   `json:"default" doc:"Value without the config file."`
	FileValue *string  `json:"fileValue,omitempty" doc:"Value in the config file, if set there."`
	Source    string   `json:"source" enum:"flag,env,file,default"`
	Locked    bool     `json:"locked" doc:"Set by a command line flag or environment variable, so not editable."`
}

// Config is the server configuration as shown in the UI.
type Config struct {
	File           string    `json:"file" doc:"Path of the config file."`
	RestartPending bool      `json:"restartPending" doc:"Saved settings wait for a restart to apply."`
	Settings       []Setting `json:"settings" nullable:"false"`
}

// ConfigUpdates maps setting names to new values, null removing the setting
// from the config file.
type ConfigUpdates map[string]*string

// Schema describes the nullable values, which Huma cannot infer for maps.
func (ConfigUpdates) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:                 huma.TypeObject,
		Description:          "Setting names mapped to new values; null removes a setting from the config file (back to default).",
		AdditionalProperties: &huma.Schema{Type: huma.TypeString, Nullable: true},
	}
}

// Health reports that the server is up.
type Health struct {
	OK        bool      `json:"ok"`
	StartedAt time.Time `json:"startedAt"`
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
	s.register(newAPI(mux))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", s.serveUI)
	// The UI has no login, so it must at least not be usable from other
	// web sites: browsers reject cross-origin requests that change
	// something.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Logger.Warn("cross-origin request rejected", "method", r.Method, "url", r.URL.String(), "origin", r.Header.Get("Origin"))
		writeProblem(w, http.StatusForbidden, "cross-origin request rejected")
	}))
	return s.logRequests(noSniff(cop.Handler(mux)))
}

// noSniff stops browsers from guessing content types, so an uploaded HTML
// file is never run as a page of the UI.
func noSniff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// OpenAPI returns the OpenAPI document of the API.
func OpenAPI() *huma.OpenAPI {
	api := newAPI(http.NewServeMux())
	(&Server{}).register(api)
	return api.OpenAPI()
}

func newAPI(mux *http.ServeMux) huma.API {
	config := huma.DefaultConfig("just-dlna admin API", "1.0.0")
	config.OpenAPIPath = "/api/openapi"
	config.DocsPath = "/api/docs"
	// No $schema links in the responses.
	config.SchemasPath = ""
	config.CreateHooks = nil
	return humago.New(mux, config)
}

// response is an operation output with a JSON body.
type response[T any] struct{ Body T }

func (s *Server) register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Check that the server is up",
		Tags:        []string{"server"},
	}, s.health)
	huma.Register(api, huma.Operation{
		OperationID: "getConfig",
		Method:      http.MethodGet,
		Path:        "/api/config",
		Summary:     "Get the settings",
		Tags:        []string{"server"},
	}, s.getConfig)
	huma.Register(api, huma.Operation{
		OperationID: "saveConfig",
		Method:      http.MethodPut,
		Path:        "/api/config",
		Summary:     "Save settings to the config file",
		Description: "The saved settings apply after a restart.",
		Tags:        []string{"server"},
		Errors:      []int{http.StatusBadRequest, http.StatusConflict},
	}, s.putConfig)
	huma.Register(api, huma.Operation{
		OperationID:   "restart",
		Method:        http.MethodPost,
		Path:          "/api/restart",
		Summary:       "Restart the server to apply saved settings",
		Tags:          []string{"server"},
		DefaultStatus: http.StatusAccepted,
	}, s.restart)
	s.registerFiles(api)
}

func (s *Server) health(ctx context.Context, _ *struct{}) (*response[Health], error) {
	return &response[Health]{Health{OK: true, StartedAt: s.started}}, nil
}

func (s *Server) config(ctx context.Context) (*response[Config], error) {
	file, settings, err := s.Config.Settings()
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	if settings == nil {
		settings = []Setting{}
	}
	s.mu.Lock()
	pending := s.restartPending
	s.mu.Unlock()
	return &response[Config]{Config{File: file, RestartPending: pending, Settings: settings}}, nil
}

func (s *Server) getConfig(ctx context.Context, _ *struct{}) (*response[Config], error) {
	return s.config(ctx)
}

func (s *Server) putConfig(ctx context.Context, in *struct{ Body ConfigUpdates }) (*response[Config], error) {
	if err := s.Config.Save(in.Body); err != nil {
		return nil, s.fail(ctx, err)
	}
	if len(in.Body) > 0 {
		s.mu.Lock()
		s.restartPending = true
		s.mu.Unlock()
	}
	return s.config(ctx)
}

func (s *Server) restart(ctx context.Context, _ *struct{}) (*struct{}, error) {
	s.Logger.Info("restart requested from the web UI")
	// The request context ends once the response has been sent.
	context.AfterFunc(ctx, s.Restart)
	return nil, nil
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

// writeProblem writes an error in the format of the API's errors.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(&huma.ErrorModel{
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// fail logs err and returns it as an API error with a matching status.
func (s *Server) fail(ctx context.Context, err error) error {
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
	attrs := []any{"status", status, "error", err}
	if r, ok := ctx.Value(requestKey{}).(*http.Request); ok {
		attrs = append([]any{"method", r.Method, "url", r.URL.String()}, attrs...)
	}
	s.Logger.Log(ctx, level, "request failed", attrs...)
	return huma.NewError(status, err.Error())
}

// requestKey is the context key of the request, for logging.
type requestKey struct{}

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
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), requestKey{}, r)))
		s.Logger.Debug("http",
			"method", r.Method,
			"url", r.URL.String(),
			"status", sw.status,
			"took", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}
