// Package library exposes a media folder exactly as it is on disk: folders are
// containers, video files are items. No naming or layout rules are imposed.
package library

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// videoMIME maps lower-case file extensions to MIME types for files we serve as video.
var videoMIME = map[string]string{
	".mkv":  "video/x-matroska",
	".mk3d": "video/x-matroska",
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".divx": "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".asf":  "video/x-ms-asf",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".vob":  "video/mpeg",
	".ts":   "video/mp2t",
	".m2ts": "video/mp2t",
	".mts":  "video/mp2t",
	".webm": "video/webm",
	".flv":  "video/x-flv",
	".3gp":  "video/3gpp",
	".ogv":  "video/ogg",
}

// VideoMIME returns the MIME type for a video file name, or "" if it is not a video.
func VideoMIME(name string) string {
	return videoMIME[strings.ToLower(path.Ext(name))]
}

// Entry is a folder or a video file inside the library.
type Entry struct {
	Path    string // slash-separated path relative to the root, "." for the root
	Name    string // actual file/folder name as on disk
	IsDir   bool
	Size    int64
	ModTime time.Time
	MIME    string // video MIME type, empty for folders
}

// Library provides traversal-safe read-only access to a media folder.
type Library struct {
	root   *os.Root
	fsys   fs.FS
	logger *slog.Logger
	probe  *prober
}

// Open opens dir as the media root.
func Open(dir string, logger *slog.Logger) (*Library, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open media root %q: %w", dir, err)
	}
	return &Library{
		root:   root,
		fsys:   root.FS(),
		logger: logger,
		probe:  newProber(logger),
	}, nil
}

// Close releases the root handle.
func (l *Library) Close() error { return l.root.Close() }

// FS returns a traversal-safe filesystem rooted at the media folder.
func (l *Library) FS() fs.FS { return l.fsys }

// AbsPath returns the absolute on-disk path of rel (used for ffmpeg/ffprobe).
func (l *Library) AbsPath(rel string) string {
	return filepath.Join(l.root.Name(), filepath.FromSlash(rel))
}

// Clean normalises a client-supplied relative path and rejects anything that
// would escape the root.
func Clean(p string) (string, error) {
	p = path.Clean("/" + strings.TrimPrefix(p, "./"))
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ".", nil
	}
	if !fs.ValidPath(p) {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return p, nil
}

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

// Stat returns the entry at rel.
func (l *Library) Stat(rel string) (Entry, error) {
	rel, err := Clean(rel)
	if err != nil {
		return Entry{}, err
	}
	fi, err := fs.Stat(l.fsys, rel)
	if err != nil {
		return Entry{}, err
	}
	e := toEntry(rel, fi)
	if !e.IsDir && e.MIME == "" {
		return Entry{}, fmt.Errorf("%q is not a video file: %w", rel, fs.ErrNotExist)
	}
	return e, nil
}

func toEntry(rel string, fi fs.FileInfo) Entry {
	name := fi.Name()
	if rel == "." {
		name = "Root"
	}
	e := Entry{Path: rel, Name: name, IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime()}
	if !e.IsDir {
		e.MIME = VideoMIME(fi.Name())
	}
	return e
}

// List returns the visible children of the folder rel: sub-folders that
// (recursively) contain at least one video, and video files. Folders first,
// then files, each sorted case-insensitively by name.
func (l *Library) List(rel string) ([]Entry, error) {
	rel, err := Clean(rel)
	if err != nil {
		return nil, err
	}
	des, err := fs.ReadDir(l.fsys, rel)
	if err != nil {
		return nil, err
	}
	var dirs, files []Entry
	for _, de := range des {
		if hidden(de.Name()) {
			continue
		}
		child := path.Join(rel, de.Name())
		fi, err := fs.Stat(l.fsys, child) // follows symlinks inside the root
		if err != nil {
			l.logger.Warn("skipping unreadable entry", "path", child, "error", err)
			continue
		}
		e := toEntry(child, fi)
		switch {
		case e.IsDir:
			if l.containsVideo(child, 0) {
				dirs = append(dirs, e)
			}
		case e.MIME != "" && fi.Mode().IsRegular():
			files = append(files, e)
		}
	}
	byName := func(s []Entry) {
		sort.Slice(s, func(i, j int) bool { return strings.ToLower(s[i].Name) < strings.ToLower(s[j].Name) })
	}
	byName(dirs)
	byName(files)
	return append(dirs, files...), nil
}

// ChildCount returns the number of visible children of a folder.
func (l *Library) ChildCount(rel string) int {
	es, err := l.List(rel)
	if err != nil {
		return 0
	}
	return len(es)
}

var errFound = errors.New("found")

// containsVideo reports whether the folder contains a video at any depth.
func (l *Library) containsVideo(rel string, depth int) bool {
	if depth > 32 { // guard against symlink loops
		return false
	}
	err := fs.WalkDir(l.fsys, rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != rel && hidden(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if fi, err := fs.Stat(l.fsys, p); err == nil && fi.IsDir() {
				if l.containsVideo(p, depth+1) {
					return errFound
				}
				return nil
			}
		}
		if !d.IsDir() && VideoMIME(d.Name()) != "" {
			return errFound
		}
		return nil
	})
	return errors.Is(err, errFound)
}
