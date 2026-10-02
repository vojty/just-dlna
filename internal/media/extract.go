package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"just-dlna/internal/library"
)

const extractTimeout = 15 * time.Minute

// Extractor converts subtitles to SRT with ffmpeg and caches the result on
// disk. Only subtitle streams are touched; video is never transcoded.
type Extractor struct {
	lib      *library.Library
	cacheDir string
	charset  string // ffmpeg -sub_charenc for non-UTF-8 text subtitle files
	logger   *slog.Logger

	mu       sync.Mutex
	inflight map[string]*job
	sem      chan struct{} // limits concurrent ffmpeg processes
}

type job struct {
	done chan struct{}
	err  error
}

// NewExtractor creates the cache directory if needed.
func NewExtractor(lib *library.Library, cacheDir, charset string, logger *slog.Logger) (*Extractor, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &Extractor{
		lib:      lib,
		cacheDir: cacheDir,
		charset:  charset,
		logger:   logger,
		inflight: map[string]*job{},
		sem:      make(chan struct{}, 2),
	}, nil
}

// cacheKey identifies the source of a converted subtitle, including size and
// mtime so edited files are re-extracted.
func (e *Extractor) cacheKey(rel string) (string, error) {
	fi, err := fs.Stat(e.lib.FS(), rel)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(rel + "\x00" + strconv.FormatInt(fi.Size(), 10) + "\x00" + fi.ModTime().UTC().String()))
	return hex.EncodeToString(h[:16]), nil
}

// embeddedPath is the cache file for embedded subtitle stream n of a video.
func (e *Extractor) embeddedPath(key string, stream int) string {
	return filepath.Join(e.cacheDir, key+".s"+strconv.Itoa(stream)+".srt")
}

// SRT returns the path of a cached SRT conversion of sub (which belongs to the
// video at rel), running ffmpeg if necessary.
func (e *Extractor) SRT(video string, sub library.Subtitle) (string, error) {
	if sub.External {
		key, err := e.cacheKey(sub.FilePath)
		if err != nil {
			return "", err
		}
		out := filepath.Join(e.cacheDir, key+".srt")
		err = e.once(out, out, func() error {
			args := []string{}
			if e.charset != "" {
				args = append(args, "-sub_charenc", e.charset)
			}
			args = append(args, "-i", e.lib.AbsPath(sub.FilePath), "-c:s", "srt", "-f", "srt", out)
			return e.ffmpeg(sub.FilePath, args, []string{out})
		})
		return out, err
	}

	key, err := e.cacheKey(video)
	if err != nil {
		return "", err
	}
	out := e.embeddedPath(key, sub.Stream)
	// all embedded streams are extracted together, so serialise on the video
	err = e.once(key, out, func() error { return e.extractAll(video, key) })
	return out, err
}

// Prefetch extracts the embedded text subtitles of a video in the background
// so they are ready when playback starts.
func (e *Extractor) Prefetch(video string, subs []library.Subtitle) {
	var first *library.Subtitle
	for i := range subs {
		if !subs[i].External {
			first = &subs[i]
			break
		}
	}
	if first == nil {
		return
	}
	go func() {
		if _, err := e.SRT(video, *first); err != nil {
			e.logger.Debug("subtitle prefetch failed", "path", video, "error", err)
		}
	}()
}

// extractAll writes every embedded text subtitle stream of a video in a single
// ffmpeg pass, so large files are only read once.
func (e *Extractor) extractAll(video, key string) error {
	subs, err := e.lib.Subtitles(video)
	if err != nil {
		return err
	}
	args := []string{"-i", e.lib.AbsPath(video)}
	var outs []string
	for _, s := range subs {
		if s.External {
			continue
		}
		out := e.embeddedPath(key, s.Stream)
		args = append(args, "-map", "0:s:"+strconv.Itoa(s.Stream), "-c:s", "srt", "-f", "srt", out)
		outs = append(outs, out)
	}
	if len(outs) == 0 {
		return errors.New("no embedded text subtitles")
	}
	return e.ffmpeg(video, args, outs)
}

// once runs fn unless out already exists, deduplicating concurrent callers
// sharing the same lock key.
func (e *Extractor) once(lockKey, out string, fn func() error) error {
	if _, err := os.Stat(out); err == nil {
		return nil
	}
	e.mu.Lock()
	if j, ok := e.inflight[lockKey]; ok {
		e.mu.Unlock()
		<-j.done
		if j.err != nil {
			return j.err
		}
		_, err := os.Stat(out)
		return err
	}
	j := &job{done: make(chan struct{})}
	e.inflight[lockKey] = j
	e.mu.Unlock()

	j.err = fn()
	if j.err == nil {
		if _, err := os.Stat(out); err != nil {
			j.err = fmt.Errorf("ffmpeg produced no output: %w", err)
		}
	}
	e.mu.Lock()
	delete(e.inflight, lockKey)
	e.mu.Unlock()
	close(j.done)
	return j.err
}

// ffmpeg runs ffmpeg writing to temporary files and renames them into outs on
// success, so readers never see partial files.
func (e *Extractor) ffmpeg(src string, args []string, outs []string) error {
	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	tmp := make([]string, len(outs))
	for i, o := range outs {
		tmp[i] = o + ".part"
	}
	// replace output paths with temporary ones
	for i, a := range args {
		for j, o := range outs {
			if a == o {
				args[i] = tmp[j]
			}
		}
	}
	// Inputs are files in the media folder; the whitelist stops crafted
	// files (e.g. an HLS playlist named .srt) from making ffmpeg open URLs.
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file"}, args...)

	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	e.logger.Info("extracting subtitles", "path", src, "outputs", len(outs))
	e.logger.Debug("ffmpeg command", "args", full)
	err := cmd.Run()
	if err != nil {
		for _, t := range tmp {
			os.Remove(t)
		}
		e.logger.Error("ffmpeg failed", "path", src, "error", err, "stderr", stderr.String(), "took", time.Since(start))
		return fmt.Errorf("ffmpeg: %w", err)
	}
	for i, t := range tmp {
		if err := os.Rename(t, outs[i]); err != nil {
			return err
		}
	}
	e.logger.Info("subtitles extracted", "path", src, "outputs", len(outs), "took", time.Since(start))
	return nil
}
