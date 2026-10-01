package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeStore struct{ saved map[string]*string }

func (f *fakeStore) Settings() (string, []Setting, error) {
	return "test.yaml", []Setting{{Name: "name", Type: "string"}}, nil
}

func (f *fakeStore) Save(u map[string]*string) error {
	if _, ok := u["locked"]; ok {
		return Conflict(os.ErrPermission)
	}
	f.saved = u
	return nil
}

func newTestServer(t *testing.T) (*httptest.Server, string, *fakeStore, chan struct{}) {
	t.Helper()
	media := t.TempDir()
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("<html>app</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	restarted := make(chan struct{}, 1)
	s, err := New(media, ui, store, func() { restarted <- struct{}{} }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, media, store, restarted
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func upload(t *testing.T, url, name, content string) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write([]byte(content))
	mw.Close()
	resp, err := http.Post(url, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestFiles(t *testing.T) {
	ts, media, _, _ := newTestServer(t)
	api := ts.URL + "/api/files"

	if code, body := do(t, "POST", api+"/mkdir", `{"path":"Movies"}`); code != 201 {
		t.Fatalf("mkdir: %d %s", code, body)
	}
	if code, _ := do(t, "POST", api+"/mkdir", `{"path":"Movies"}`); code != 409 {
		t.Errorf("mkdir existing: %d, want 409", code)
	}
	if code := upload(t, api+"/upload?path=Movies", "a.mkv", "video"); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	if code := upload(t, api+"/upload?path=Movies", "a.mkv", "other"); code != 409 {
		t.Errorf("upload conflict: %d, want 409", code)
	}
	if code := upload(t, api+"/upload?path=Movies&overwrite=1", "a.mkv", "video2"); code != 201 {
		t.Errorf("upload overwrite: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(media, "Movies", "a.mkv")); string(b) != "video2" {
		t.Errorf("uploaded content %q", b)
	}
	if code := upload(t, api+"/upload?path=Movies", ".hidden", "x"); code != 400 {
		t.Errorf("upload hidden name: %d, want 400", code)
	}

	code, body := do(t, "GET", api+"?path=Movies", "")
	var list struct{ Entries []FileEntry }
	json.Unmarshal([]byte(body), &list)
	if code != 200 || len(list.Entries) != 1 || list.Entries[0].Kind != "video" || list.Entries[0].Path != "Movies/a.mkv" {
		t.Errorf("list: %d %s", code, body)
	}

	if code, body := do(t, "GET", api+"/download?path=Movies/a.mkv", ""); code != 200 || body != "video2" {
		t.Errorf("download: %d %q", code, body)
	}

	if code, body := do(t, "POST", api+"/move", `{"from":"Movies/a.mkv","to":"b.mkv"}`); code != 200 {
		t.Errorf("move: %d %s", code, body)
	}
	if code, _ := do(t, "POST", api+"/move", `{"from":"Movies","to":"Movies/x"}`); code != 400 {
		t.Errorf("move into itself: %d, want 400", code)
	}

	if code, _ := do(t, "DELETE", api+"?path=Movies", ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(media, "Movies")); !os.IsNotExist(err) {
		t.Errorf("Movies still exists: %v", err)
	}
	if code, _ := do(t, "DELETE", api+"?path=", ""); code != 400 {
		t.Errorf("delete root: %d, want 400", code)
	}
	if code, _ := do(t, "DELETE", api+"?path=missing", ""); code != 404 {
		t.Errorf("delete missing: %d, want 404", code)
	}
}

func TestTraversal(t *testing.T) {
	ts, media, _, _ := newTestServer(t)
	outside := filepath.Join(filepath.Dir(media), "outside.txt")
	os.WriteFile(outside, []byte("secret"), 0o644)
	os.Symlink(filepath.Dir(media), filepath.Join(media, "link"))

	for _, p := range []string{"../outside.txt", "link/outside.txt"} {
		if code, body := do(t, "GET", ts.URL+"/api/files/download?path="+p, ""); code == 200 {
			t.Errorf("download %s: got %q", p, body)
		}
	}
	do(t, "POST", ts.URL+"/api/files/move", `{"from":"../outside.txt","to":"stolen.txt"}`)
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("file outside the media folder was moved: %v", err)
	}
}

func TestConfigAndUI(t *testing.T) {
	ts, _, store, restarted := newTestServer(t)

	if code, body := do(t, "PUT", ts.URL+"/api/config", `{"name":"TV","sub-charset":null}`); code != 200 || !strings.Contains(body, `"restartPending":true`) {
		t.Errorf("put config: %d %s", code, body)
	}
	if v := store.saved["name"]; v == nil || *v != "TV" {
		t.Errorf("saved %v", store.saved)
	}
	if _, ok := store.saved["sub-charset"]; !ok {
		t.Error("null value not passed to the store")
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/config", `{"locked":"x"}`); code != 409 {
		t.Errorf("put locked: %d, want 409", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/api/restart", ""); code != 202 {
		t.Errorf("restart: %d", code)
	}
	<-restarted

	for _, p := range []string{"/", "/files", "/settings?x=1"} {
		if code, body := do(t, "GET", ts.URL+p, ""); code != 200 || !strings.Contains(body, "app") {
			t.Errorf("GET %s: %d %q", p, code, body)
		}
	}
	if code, _ := do(t, "GET", ts.URL+"/api/nope", ""); code != 404 {
		t.Errorf("unknown api: %d, want 404", code)
	}
}
