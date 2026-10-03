package media

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"just-dlna/internal/library"
	"just-dlna/internal/profile"
)

// ContentFeatures is the DLNA contentFeatures value for files streamed as-is:
// byte seeking supported, not converted, streaming transfer mode.
const ContentFeatures = "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"

// Server serves videos and subtitles.
type Server struct {
	Lib       *library.Library
	Extractor *Extractor
	Port      int
	Logger    *slog.Logger
	// Interfaces limits which interfaces' addresses BaseURL may put into
	// URLs; nil allows all.
	Interfaces []string
}

// Handler returns the HTTP handler with request logging.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{path...}", s.serveVideo)
	mux.HandleFunc("GET /sub/{n}/{path...}", s.serveSubtitle)
	return s.logRequests(mux)
}

// Subtitles returns the subtitles of a video with URLs relative to base.
func (s *Server) Subtitles(base, rel string) ([]profile.SubtitleRef, error) {
	subs, err := s.Lib.Subtitles(rel)
	if err != nil {
		return nil, err
	}
	refs := make([]profile.SubtitleRef, len(subs))
	for i, sub := range subs {
		refs[i] = profile.SubtitleRef{Subtitle: sub, URL: SubtitleURL(base, rel, sub.Index)}
	}
	return refs, nil
}

func (s *Server) serveVideo(w http.ResponseWriter, r *http.Request) {
	rel, err := library.Clean(r.PathValue("path"))
	if err != nil {
		s.httpError(w, r, http.StatusBadRequest, err)
		return
	}
	entry, err := s.Lib.Stat(rel)
	if err != nil || entry.IsDir {
		s.httpError(w, r, http.StatusNotFound, err)
		return
	}
	f, err := s.Lib.FS().Open(rel)
	if err != nil {
		s.httpError(w, r, http.StatusNotFound, err)
		return
	}
	defer f.Close()

	prof := profile.ForUserAgent(r.UserAgent())
	h := w.Header()
	h.Set("Content-Type", entry.MIME)
	h.Set("contentFeatures.dlna.org", ContentFeatures)
	h.Set("transferMode.dlna.org", transferMode(r, "Streaming"))
	h.Set("Accept-Ranges", "bytes")
	if subs, err := s.Subtitles(s.BaseURL(r.Host), rel); err == nil {
		prof.VideoHeaders(h, r, subs)
	}
	s.Logger.Debug("serving video", "path", rel, "profile", prof.Name(), "range", r.Header.Get("Range"))
	http.ServeContent(w, r, entry.Name, entry.ModTime, f.(io.ReadSeeker))
}

func (s *Server) serveSubtitle(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		s.httpError(w, r, http.StatusBadRequest, err)
		return
	}
	rel, err := library.Clean(strings.TrimSuffix(r.PathValue("path"), ".srt"))
	if err != nil {
		s.httpError(w, r, http.StatusBadRequest, err)
		return
	}
	subs, err := s.Lib.Subtitles(rel)
	if err != nil {
		s.httpError(w, r, http.StatusNotFound, err)
		return
	}
	if n < 0 || n >= len(subs) {
		s.httpError(w, r, http.StatusNotFound, errors.New("no such subtitle"))
		return
	}
	sub := subs[n]

	var (
		data    []byte
		modTime time.Time
	)
	if sub.External && sub.Format == "srt" {
		data, err = fs.ReadFile(s.Lib.FS(), sub.FilePath)
		if err == nil {
			if fi, serr := fs.Stat(s.Lib.FS(), sub.FilePath); serr == nil {
				modTime = fi.ModTime()
			}
		}
		// Re-encode legacy-charset files to UTF-8 when a charset is configured.
		if err == nil && !utf8.Valid(data) && s.Extractor.charset != "" {
			data, modTime, err = s.convert(rel, sub)
		}
	} else {
		data, modTime, err = s.convert(rel, sub)
	}
	if err != nil {
		s.httpError(w, r, http.StatusInternalServerError, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/srt; charset=utf-8")
	h.Set("transferMode.dlna.org", transferMode(r, "Background"))
	h.Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0")
	h.Set("Access-Control-Allow-Origin", "*")
	s.Logger.Debug("serving subtitle", "path", rel, "index", n, "title", sub.Title, "external", sub.External)
	http.ServeContent(w, r, "", modTime, bytes.NewReader(data))
}

func (s *Server) convert(rel string, sub library.Subtitle) ([]byte, time.Time, error) {
	p, err := s.Extractor.SRT(rel, sub)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, time.Time{}, err
	}
	var mt time.Time
	if fi, err := os.Stat(p); err == nil {
		mt = fi.ModTime()
	}
	return data, mt, nil
}

// transferMode echoes the client's requested DLNA transfer mode or uses def.
func transferMode(r *http.Request, def string) string {
	if m := r.Header.Get("transferMode.dlna.org"); m != "" {
		return m
	}
	return def
}

func (s *Server) httpError(w http.ResponseWriter, r *http.Request, code int, err error) {
	if err == nil {
		err = errors.New(http.StatusText(code))
	}
	level := slog.LevelWarn
	if code >= 500 {
		level = slog.LevelError
	}
	s.Logger.Log(r.Context(), level, "request failed", "method", r.Method, "url", r.URL.Path, "status", code, "error", err)
	http.Error(w, http.StatusText(code), code)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.Logger.Debug("http",
			"method", r.Method,
			"url", r.URL.Path,
			"range", r.Header.Get("Range"),
			"status", sw.status,
			"bytes", sw.bytes,
			"took", time.Since(start),
			"remote", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
	})
}
