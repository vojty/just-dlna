package library

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/ffprobe"
)

// MediaInfo is the subset of ffprobe output the server uses.
type MediaInfo struct {
	Duration time.Duration
	Bitrate  uint // bits per second
	Width    int
	Height   int
	Streams  []StreamInfo
}

// StreamInfo describes one stream of a media file.
type StreamInfo struct {
	CodecType string // video, audio, subtitle, ...
	CodecName string
	Language  string
	Title     string
	Default   bool
	Forced    bool
}

type probeKey struct {
	path    string
	size    int64
	modTime time.Time
}

type probeResult struct {
	info *MediaInfo
	err  error
}

// prober runs ffprobe and caches results keyed by path, size and mtime.
type prober struct {
	logger *slog.Logger
	mu     sync.Mutex
	cache  map[string]probeEntry
	warned bool
}

type probeEntry struct {
	key probeKey
	res probeResult
}

func newProber(logger *slog.Logger) *prober {
	return &prober{logger: logger, cache: map[string]probeEntry{}}
}

// Probe returns media information for the video at rel.
func (l *Library) Probe(rel string) (*MediaInfo, error) { return l.probe.Probe(l, rel) }

func (p *prober) Probe(l *Library, rel string) (*MediaInfo, error) {
	fi, err := fs.Stat(l.fsys, rel)
	if err != nil {
		return nil, err
	}
	key := probeKey{rel, fi.Size(), fi.ModTime()}
	p.mu.Lock()
	if e, ok := p.cache[rel]; ok && e.key == key {
		p.mu.Unlock()
		return e.res.info, e.res.err
	}
	p.mu.Unlock()

	start := time.Now()
	raw, err := ffprobe.Run(l.AbsPath(rel))
	var res probeResult
	if err != nil {
		if errors.Is(err, ffprobe.ExeNotFound) {
			p.mu.Lock()
			if !p.warned {
				p.logger.Warn("ffprobe not found: embedded subtitles and media details disabled")
				p.warned = true
			}
			p.mu.Unlock()
		} else {
			p.logger.Error("ffprobe failed", "path", rel, "error", err)
		}
		res.err = fmt.Errorf("probe %q: %w", rel, err)
	} else {
		res.info = convert(raw)
		p.logger.Debug("probed", "path", rel, "took", time.Since(start), "streams", len(res.info.Streams))
	}

	p.mu.Lock()
	p.cache[rel] = probeEntry{key, res}
	p.mu.Unlock()
	return res.info, res.err
}

func convert(raw *ffprobe.Info) *MediaInfo {
	mi := &MediaInfo{}
	if d, err := raw.Duration(); err == nil {
		mi.Duration = d
	}
	if b, err := raw.Bitrate(); err == nil {
		mi.Bitrate = b
	}
	for _, s := range raw.Streams {
		si := StreamInfo{
			CodecType: str(s["codec_type"]),
			CodecName: str(s["codec_name"]),
		}
		if tags, ok := s["tags"].(map[string]any); ok {
			si.Language = str(tags["language"])
			si.Title = str(tags["title"])
		}
		if disp, ok := s["disposition"].(map[string]any); ok {
			si.Default = num(disp["default"]) == 1
			si.Forced = num(disp["forced"]) == 1
		}
		if si.CodecType == "video" && mi.Width == 0 {
			mi.Width = int(num(s["width"]))
			mi.Height = int(num(s["height"]))
		}
		mi.Streams = append(mi.Streams, si)
	}
	return mi
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int64 {
	n, _ := ffprobe.AnyAsInt64(v)
	return n
}
