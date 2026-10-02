package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func open(t *testing.T, root string) *Library {
	t.Helper()
	l, err := Open(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func externalTitles(subs []Subtitle) []string {
	var out []string
	for _, s := range subs {
		if s.External {
			out = append(out, s.FilePath)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCleanRejectsTraversal(t *testing.T) {
	for in, want := range map[string]string{
		"":              ".",
		".":             ".",
		"./":            ".",
		"a/b.mkv":       "a/b.mkv",
		"../etc/passwd": "etc/passwd",
		"/a/../../b":    "b",
	} {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{".hidden.mkv", "a/.git/config", ".x/../.y/b.mkv"} {
		if got, err := Clean(in); err == nil {
			t.Errorf("Clean(%q) = %q, want an error for a hidden path", in, got)
		}
	}
}

func TestListMirrorsTree(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Movies/Žluťoučký kůň (2020)/film.MKV",
		"Movies/random notes.txt",
		"empty/deeper/readme.txt",
		".hidden/x.mkv",
		".secret.mp4",
		"b clip.mp4",
		"A clip.avi",
	)
	l := open(t, root)

	got, err := l.List(".")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Movies", "A clip.avi", "b clip.mp4"}; !equal(names(got), want) {
		t.Errorf("root = %v, want %v", names(got), want)
	}
	got, err = l.List("Movies/Žluťoučký kůň (2020)")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "Movies/Žluťoučký kůň (2020)/film.MKV" || got[0].MIME != "video/x-matroska" {
		t.Errorf("unexpected entries %+v", got)
	}
}

func TestSidecarMatching(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"show/ep1.mkv", "show/ep10.mkv",
		"show/ep1.srt", "show/EP1.cz.forced.srt", "show/ep1_en.ass",
		"show/ep10.srt", "show/other.srt",
		"lone/some movie.mp4", "lone/whatever.srt", "lone/SUBS/cz.vtt", "lone/Subs.txt",
	)
	l := open(t, root)

	subs, err := l.Subtitles("show/ep1.mkv")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"show/ep1.srt", "show/EP1.cz.forced.srt", "show/ep1_en.ass"}
	if got := externalTitles(subs); !equal(got, want) {
		t.Errorf("ep1 subs = %v, want %v", got, want)
	}
	if subs[1].Lang != "cz" || !subs[1].Forced {
		t.Errorf("expected cz forced, got %+v", subs[1])
	}
	if subs[2].Lang != "en" || subs[2].Format != "ass" {
		t.Errorf("expected en ass, got %+v", subs[2])
	}
	for i, s := range subs {
		if s.Index != i {
			t.Errorf("sub %d has index %d", i, s.Index)
		}
	}

	subs, _ = l.Subtitles("show/ep10.mkv")
	if got := externalTitles(subs); !equal(got, []string{"show/ep10.srt"}) {
		t.Errorf("ep10 subs = %v", got)
	}

	// A lone video gets every subtitle in its folder and Subs/ sub-folder.
	subs, _ = l.Subtitles("lone/some movie.mp4")
	if got := externalTitles(subs); !equal(got, []string{"lone/SUBS/cz.vtt", "lone/whatever.srt"}) {
		t.Errorf("lone subs = %v", got)
	}
}
