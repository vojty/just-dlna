package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"just-dlna/internal/library"
)

// FileEntry is a file or folder in the media folder.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // slash-separated, relative to the media folder
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Kind    string    `json:"kind"` // folder, video, subtitle or other
}

func kind(name string, isDir bool) string {
	switch {
	case isDir:
		return "folder"
	case library.VideoMIME(name) != "":
		return "video"
	case library.IsSubtitle(name):
		return "subtitle"
	default:
		return "other"
	}
}

// cleanPath normalises a client-supplied path; os.Root additionally rejects
// anything escaping the media folder, including via symlinks.
func cleanPath(p string) (string, error) {
	rel, err := library.Clean(p)
	if err != nil {
		return "", Invalid(err)
	}
	return rel, nil
}

// validName checks a single file or folder name chosen by the client.
func validName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return Invalid(fmt.Errorf("invalid name %q", name))
	case strings.ContainsAny(name, `/\`):
		return Invalid(fmt.Errorf("name %q must not contain slashes", name))
	case strings.HasPrefix(name, "."):
		return Invalid(fmt.Errorf("name %q must not start with a dot", name))
	}
	return nil
}

func newEntry(rel string, fi fs.FileInfo) FileEntry {
	return FileEntry{
		Name:    fi.Name(),
		Path:    rel,
		IsDir:   fi.IsDir(),
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		Kind:    kind(fi.Name(), fi.IsDir()),
	}
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	dir, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	des, err := fs.ReadDir(s.root.FS(), dir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	entries := []FileEntry{}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		rel := path.Join(dir, de.Name())
		fi, err := s.root.Stat(rel) // follows symlinks inside the root
		if err != nil {
			continue
		}
		entries = append(entries, newEntry(rel, fi))
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) requireDir(dir string) error {
	fi, err := s.root.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return Invalid(fmt.Errorf("%q is not a folder", dir))
	}
	return nil
}

func (s *Server) exists(rel string) bool {
	_, err := s.root.Lstat(rel)
	return err == nil
}

// upload stores the files of a multipart request in the folder given by the
// path query parameter. Each file is written to a hidden temporary file first
// and renamed when complete, so clients never see partial uploads.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, err := cleanPath(q.Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	overwrite, _ := strconv.ParseBool(q.Get("overwrite"))
	if err := s.requireDir(dir); err != nil {
		s.fail(w, r, err)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		s.fail(w, r, Invalid(err))
		return
	}
	uploaded := []FileEntry{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.fail(w, r, Invalid(err))
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		e, err := s.saveUpload(dir, part.FileName(), part, overwrite)
		part.Close()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.Logger.Info("uploaded", "path", e.Path, "size", e.Size)
		uploaded = append(uploaded, e)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"files": uploaded})
}

func (s *Server) saveUpload(dir, name string, src io.Reader, overwrite bool) (FileEntry, error) {
	// Browsers send only the base name, but be strict about anything else.
	if err := validName(name); err != nil {
		return FileEntry{}, err
	}
	rel := path.Join(dir, name)
	if !overwrite && s.exists(rel) {
		return FileEntry{}, Conflict(fmt.Errorf("%q already exists", rel))
	}
	tmp := path.Join(dir, "."+name+".part")
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return FileEntry{}, err
	}
	_, err = io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.root.Rename(tmp, rel)
	}
	if err != nil {
		_ = s.root.Remove(tmp)
		return FileEntry{}, err
	}
	fi, err := s.root.Stat(rel)
	if err != nil {
		return FileEntry{}, err
	}
	return newEntry(rel, fi), nil
}

func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return Invalid(err)
	}
	return nil
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	rel, err := cleanPath(req.Path)
	if err == nil && rel == "." {
		err = Invalid(errors.New("missing folder name"))
	}
	if err == nil {
		err = validName(path.Base(rel))
	}
	if err == nil {
		err = s.root.Mkdir(rel, 0o755)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fi, err := s.root.Stat(rel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Logger.Info("folder created", "path", rel)
	writeJSON(w, http.StatusCreated, newEntry(rel, fi))
}

// move renames or moves a file or folder.
func (s *Server) move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	from, err := cleanPath(req.From)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	to, err := cleanPath(req.To)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch {
	case from == "." || to == ".":
		err = Invalid(errors.New("the media folder itself cannot be moved"))
	case to == from:
		err = Invalid(errors.New("source and target are the same"))
	case strings.HasPrefix(to, from+"/"):
		err = Invalid(errors.New("a folder cannot be moved into itself"))
	case s.exists(to):
		err = Conflict(fmt.Errorf("%q already exists", to))
	}
	if err == nil {
		err = validName(path.Base(to))
	}
	if err == nil {
		err = s.requireDir(path.Dir(to))
	}
	if err == nil {
		err = s.root.Rename(from, to)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fi, err := s.root.Stat(to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Logger.Info("moved", "from", from, "to", to)
	writeJSON(w, http.StatusOK, newEntry(to, fi))
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanPath(r.URL.Query().Get("path"))
	if err == nil && rel == "." {
		err = Invalid(errors.New("the media folder itself cannot be deleted"))
	}
	if err == nil {
		_, err = s.root.Lstat(rel)
	}
	if err == nil {
		err = s.root.RemoveAll(rel)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Logger.Info("deleted", "path", rel)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := s.root.Open(rel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if fi.IsDir() {
		s.fail(w, r, Invalid(fmt.Errorf("%q is a folder", rel)))
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
