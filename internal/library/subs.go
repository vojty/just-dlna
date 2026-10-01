package library

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Subtitle file extensions recognised as sidecar subtitles. .sub is left out
// on purpose: it is usually a VobSub bitmap paired with .idx.
var subtitleExts = map[string]bool{
	".srt": true,
	".vtt": true,
	".ass": true,
	".ssa": true,
	".smi": true,
}

// Embedded subtitle codecs that can be converted to text. Bitmap codecs such as
// hdmv_pgs_subtitle or dvd_subtitle would need OCR and are skipped.
var textSubtitleCodecs = map[string]bool{
	"subrip":   true,
	"srt":      true,
	"ass":      true,
	"ssa":      true,
	"webvtt":   true,
	"mov_text": true,
	"text":     true,
	"microdvd": true,
	"sami":     true,
}

// subsDirNames are sub-folder names searched for subtitles of a lone video.
var subsDirNames = map[string]bool{"subs": true, "sub": true, "subtitles": true}

// Subtitle is a subtitle track available for a video, either a sidecar file or
// a stream embedded in the container.
type Subtitle struct {
	Index    int    // position in the list returned by Subtitles; used in URLs
	Lang     string // best-effort language code, may be empty
	Title    string // human readable label
	Format   string // source format: srt, vtt, ass, ssa, smi, or embedded codec name
	External bool
	FilePath string // relative path of the sidecar file (External only)
	Stream   int    // subtitle stream ordinal for ffmpeg -map 0:s:N (embedded only)
	Default  bool
	Forced   bool
}

// Subtitles lists the subtitles for the video at rel. Sidecar files come first,
// then embedded text tracks. Matching is lenient and never required:
//   - files in the same folder whose name starts with the video's base name
//   - if the folder holds exactly one video: every subtitle file in it and in a
//     Subs/Subtitles sub-folder
func (l *Library) Subtitles(rel string) ([]Subtitle, error) {
	v, err := l.Stat(rel)
	if err != nil {
		return nil, err
	}
	if v.IsDir {
		return nil, fmt.Errorf("%q is a folder", rel)
	}
	subs := l.externalSubtitles(v.Path)
	subs = append(subs, l.embeddedSubtitles(v.Path)...)
	for i := range subs {
		subs[i].Index = i
	}
	return subs, nil
}

// IsSubtitle reports whether name has a sidecar subtitle extension.
func IsSubtitle(name string) bool {
	return subtitleExts[strings.ToLower(path.Ext(name))]
}

func stem(name string) string { return strings.TrimSuffix(name, path.Ext(name)) }

func (l *Library) externalSubtitles(video string) []Subtitle {
	dir, name := path.Split(video)
	dir = path.Clean(dir)
	des, err := fs.ReadDir(l.fsys, dir)
	if err != nil {
		l.logger.Warn("cannot read folder for subtitles", "path", dir, "error", err)
		return nil
	}
	base := strings.ToLower(stem(name))

	videos := 0
	var subFiles, subDirs []string
	for _, de := range des {
		n := de.Name()
		if hidden(n) {
			continue
		}
		switch {
		case de.IsDir() && subsDirNames[strings.ToLower(n)]:
			subDirs = append(subDirs, path.Join(dir, n))
		case VideoMIME(n) != "":
			videos++
		case IsSubtitle(n):
			subFiles = append(subFiles, path.Join(dir, n))
		}
	}

	var matched []string
	if videos == 1 {
		matched = subFiles
		for _, sd := range subDirs {
			sdes, err := fs.ReadDir(l.fsys, sd)
			if err != nil {
				l.logger.Warn("cannot read subtitles folder", "path", sd, "error", err)
				continue
			}
			for _, de := range sdes {
				if !de.IsDir() && !hidden(de.Name()) && IsSubtitle(de.Name()) {
					matched = append(matched, path.Join(sd, de.Name()))
				}
			}
		}
	} else {
		for _, f := range subFiles {
			if belongsTo(stem(path.Base(f)), base) {
				matched = append(matched, f)
			}
		}
	}

	// Exact-name match first (movie.srt), then alphabetical.
	sort.SliceStable(matched, func(i, j int) bool {
		ei := strings.ToLower(stem(path.Base(matched[i]))) == base
		ej := strings.ToLower(stem(path.Base(matched[j]))) == base
		if ei != ej {
			return ei
		}
		return strings.ToLower(matched[i]) < strings.ToLower(matched[j])
	})

	out := make([]Subtitle, 0, len(matched))
	for _, f := range matched {
		fn := path.Base(f)
		lang, forced := parseSuffix(stem(fn), base)
		out = append(out, Subtitle{
			Lang:     lang,
			Title:    fn,
			Format:   strings.TrimPrefix(strings.ToLower(path.Ext(fn)), "."),
			External: true,
			FilePath: f,
			Forced:   forced,
		})
	}
	return out
}

// belongsTo reports whether a subtitle stem starts with the video base name
// followed by a separator, so "ep1.en" matches "ep1" but "ep10" does not.
func belongsTo(subStem, videoBase string) bool {
	s := strings.ToLower(subStem)
	if !strings.HasPrefix(s, videoBase) {
		return false
	}
	rest := s[len(videoBase):]
	return rest == "" || isSep(rune(rest[0]))
}

func isSep(r rune) bool {
	return r == '.' || r == '_' || r == '-' || r == ' ' || r == '[' || r == ']' || r == '(' || r == ')'
}

// parseSuffix extracts a language code and forced flag from the part of a
// subtitle file stem following the video base name, e.g. "movie.en.forced".
func parseSuffix(subStem, videoBase string) (lang string, forced bool) {
	s := strings.ToLower(subStem)
	s = strings.TrimPrefix(s, videoBase)
	tokens := strings.FieldsFunc(s, isSep)
	for _, t := range tokens {
		switch {
		case t == "forced":
			forced = true
		case lang == "" && (len(t) == 2 || len(t) == 3) && isLetters(t):
			lang = t
		}
	}
	return
}

func isLetters(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func (l *Library) embeddedSubtitles(video string) []Subtitle {
	info, err := l.probe.Probe(l, video)
	if err != nil {
		return nil // already logged by the prober
	}
	var out []Subtitle
	ordinal := -1
	for _, s := range info.Streams {
		if s.CodecType != "subtitle" {
			continue
		}
		ordinal++ // ffmpeg's 0:s:N counts all subtitle streams, including bitmap ones
		if !textSubtitleCodecs[s.CodecName] {
			l.logger.Debug("skipping non-text embedded subtitle", "path", video, "stream", ordinal, "codec", s.CodecName)
			continue
		}
		title := s.Title
		if title == "" {
			title = fmt.Sprintf("Track %d", ordinal+1)
		}
		if s.Language != "" && s.Language != "und" {
			title = s.Language + " - " + title
		}
		lang := s.Language
		if lang == "und" {
			lang = ""
		}
		out = append(out, Subtitle{
			Lang:    lang,
			Title:   title,
			Format:  s.CodecName,
			Stream:  ordinal,
			Default: s.Default,
			Forced:  s.Forced,
		})
	}
	return out
}
