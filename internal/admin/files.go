package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"just-dlna/internal/library"
)

// FileEntry is a file or folder in the media folder.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path" doc:"Slash-separated path relative to the media folder."`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Kind    string    `json:"kind" enum:"folder,video,subtitle,other"`
}

// Listing is the content of a folder.
type Listing struct {
	Path    string      `json:"path" doc:"Path of the folder, \".\" for the media folder itself."`
	Entries []FileEntry `json:"entries" nullable:"false"`
}

// Uploaded lists the files stored by an upload.
type Uploaded struct {
	Files []FileEntry `json:"files" nullable:"false"`
}

// MkdirRequest creates a folder.
type MkdirRequest struct {
	Path string `json:"path" doc:"Path of the new folder."`
}

// MoveRequest renames or moves a file or folder.
type MoveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// pathParam is a path in the media folder given as a query parameter.
type pathParam struct {
	Path string `query:"path" doc:"Slash-separated path relative to the media folder; empty for the media folder itself."`
}

func (s *Server) registerFiles(api huma.API) {
	notFound := []int{http.StatusBadRequest, http.StatusNotFound}
	huma.Register(api, huma.Operation{
		OperationID: "listFiles",
		Method:      http.MethodGet,
		Path:        "/api/files",
		Summary:     "List a folder",
		Tags:        []string{"files"},
		Errors:      notFound,
	}, s.listFiles)
	huma.Register(api, huma.Operation{
		OperationID:   "deleteFile",
		Method:        http.MethodDelete,
		Path:          "/api/files",
		Summary:       "Delete a file or folder with its content",
		Tags:          []string{"files"},
		DefaultStatus: http.StatusNoContent,
		Errors:        notFound,
	}, s.deleteFile)
	huma.Register(api, huma.Operation{
		OperationID:   "uploadFiles",
		Method:        http.MethodPost,
		Path:          "/api/files/upload",
		Summary:       "Upload files into a folder",
		Tags:          []string{"files"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
	}, s.upload)
	// Huma would read the whole body for a declared request body, so it is
	// declared after registration and upload streams the parts itself.
	api.OpenAPI().Paths["/api/files/upload"].Post.RequestBody = &huma.RequestBody{
		Required: true,
		Content: map[string]*huma.MediaType{
			"multipart/form-data": {Schema: &huma.Schema{
				Type:     huma.TypeObject,
				Required: []string{"file"},
				Properties: map[string]*huma.Schema{
					"file": {
						Type:  huma.TypeArray,
						Items: &huma.Schema{Type: huma.TypeString, Format: "binary"},
					},
				},
			}},
		},
	}
	huma.Register(api, huma.Operation{
		OperationID:   "createFolder",
		Method:        http.MethodPost,
		Path:          "/api/files/mkdir",
		Summary:       "Create a folder",
		Tags:          []string{"files"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
	}, s.mkdir)
	huma.Register(api, huma.Operation{
		OperationID: "moveFile",
		Method:      http.MethodPost,
		Path:        "/api/files/move",
		Summary:     "Rename or move a file or folder",
		Tags:        []string{"files"},
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
	}, s.move)
	huma.Register(api, huma.Operation{
		OperationID: "downloadFile",
		Method:      http.MethodGet,
		Path:        "/api/files/download",
		Summary:     "Download a file",
		Description: "Supports range requests.",
		Tags:        []string{"files"},
		Errors:      notFound,
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The file content.",
				Content: map[string]*huma.MediaType{
					"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
				},
			},
		},
	}, s.download)
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

func (s *Server) listFiles(ctx context.Context, in *pathParam) (*response[Listing], error) {
	dir, err := cleanPath(in.Path)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	des, err := fs.ReadDir(s.root.FS(), dir)
	if err != nil {
		return nil, s.fail(ctx, err)
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
	return &response[Listing]{Listing{Path: dir, Entries: entries}}, nil
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

type uploadInput struct {
	Path      string `query:"path" doc:"Folder to upload into; empty for the media folder itself."`
	Overwrite bool   `query:"overwrite" doc:"Replace existing files."`

	req *http.Request
}

// Resolve keeps the request so upload can stream its multipart body.
func (in *uploadInput) Resolve(ctx huma.Context) []error {
	in.req, _ = humago.Unwrap(ctx)
	return nil
}

// upload stores the files of a multipart request in the folder given by the
// path query parameter. Each file is written to a hidden temporary file first
// and renamed when complete, so clients never see partial uploads.
func (s *Server) upload(ctx context.Context, in *uploadInput) (*response[Uploaded], error) {
	dir, err := cleanPath(in.Path)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	if err := s.requireDir(dir); err != nil {
		return nil, s.fail(ctx, err)
	}
	mr, err := in.req.MultipartReader()
	if err != nil {
		return nil, s.fail(ctx, Invalid(err))
	}
	uploaded := []FileEntry{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, s.fail(ctx, Invalid(err))
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		e, err := s.saveUpload(dir, part.FileName(), part, in.Overwrite)
		part.Close()
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		s.Logger.Info("uploaded", "path", e.Path, "size", e.Size)
		uploaded = append(uploaded, e)
	}
	return &response[Uploaded]{Uploaded{Files: uploaded}}, nil
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

func (s *Server) mkdir(ctx context.Context, in *struct{ Body MkdirRequest }) (*response[FileEntry], error) {
	rel, err := cleanPath(in.Body.Path)
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
		return nil, s.fail(ctx, err)
	}
	fi, err := s.root.Stat(rel)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	s.Logger.Info("folder created", "path", rel)
	return &response[FileEntry]{newEntry(rel, fi)}, nil
}

// move renames or moves a file or folder.
func (s *Server) move(ctx context.Context, in *struct{ Body MoveRequest }) (*response[FileEntry], error) {
	from, err := cleanPath(in.Body.From)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	to, err := cleanPath(in.Body.To)
	if err != nil {
		return nil, s.fail(ctx, err)
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
		return nil, s.fail(ctx, err)
	}
	fi, err := s.root.Stat(to)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	s.Logger.Info("moved", "from", from, "to", to)
	return &response[FileEntry]{newEntry(to, fi)}, nil
}

func (s *Server) deleteFile(ctx context.Context, in *pathParam) (*struct{}, error) {
	rel, err := cleanPath(in.Path)
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
		return nil, s.fail(ctx, err)
	}
	s.Logger.Info("deleted", "path", rel)
	return nil, nil
}

func (s *Server) download(ctx context.Context, in *pathParam) (*huma.StreamResponse, error) {
	rel, err := cleanPath(in.Path)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	f, err := s.root.Open(rel)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	fi, err := f.Stat()
	if err == nil && fi.IsDir() {
		err = Invalid(fmt.Errorf("%q is a folder", rel))
	}
	if err != nil {
		f.Close()
		return nil, s.fail(ctx, err)
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer f.Close()
		r, w := humago.Unwrap(hctx)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}))
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	}}, nil
}
