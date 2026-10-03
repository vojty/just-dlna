// Package cds implements the ContentDirectory Browse hooks of dms, mapping the
// media folder to DIDL-Lite containers and items.
package cds

import (
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strconv"
	"sync"

	"just-dlna/internal/didl"
	"just-dlna/internal/library"
	"just-dlna/internal/media"
	"just-dlna/internal/profile"
)

// Browser builds DIDL objects for dms.
type Browser struct {
	Lib          *library.Library
	Media        *media.Server
	PrefetchSubs bool // extract embedded subtitles when an item's metadata is requested
	Logger       *slog.Logger
}

// Object IDs follow dms's scheme: "0" for the root, otherwise the
// query-escaped relative path. dms decodes them before calling the hooks.
func objectID(rel string) string {
	if rel == "." {
		return "0"
	}
	return url.QueryEscape(rel)
}

func parentID(rel string) string {
	if rel == "." {
		return "-1"
	}
	return objectID(path.Dir(rel))
}

// BrowseDirectChildren is dms's OnBrowseDirectChildren hook.
func (b *Browser) BrowseDirectChildren(p, _ string, host, userAgent string) ([]any, error) {
	rel, err := library.Clean(p)
	if err != nil {
		return nil, err
	}
	entries, err := b.Lib.List(rel)
	if err != nil {
		b.Logger.Error("browse failed", "path", rel, "error", err)
		return nil, err
	}
	prof := profile.ForUserAgent(userAgent)
	b.Logger.Debug("browse children", "path", rel, "entries", len(entries), "profile", prof.Name(), "user_agent", userAgent)
	// Build objects concurrently: the first browse of a folder runs ffprobe
	// on every video, which would be slow enough for clients to time out.
	objs := make([]any, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, e := range entries {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			objs[i] = b.object(e, host, prof, false)
		})
	}
	wg.Wait()
	return objs, nil
}

// BrowseMetadata is dms's OnBrowseMetadata hook.
func (b *Browser) BrowseMetadata(p, _ string, host, userAgent string) (any, error) {
	rel, err := library.Clean(p)
	if err != nil {
		return nil, err
	}
	e, err := b.Lib.Stat(rel)
	if err != nil {
		b.Logger.Warn("metadata failed", "path", rel, "error", err)
		return nil, err
	}
	prof := profile.ForUserAgent(userAgent)
	b.Logger.Debug("browse metadata", "path", rel, "profile", prof.Name(), "user_agent", userAgent)
	// Metadata is typically requested right before playback: warm the
	// subtitle cache so extraction from large files doesn't delay it.
	return b.object(e, host, prof, b.PrefetchSubs), nil
}

func (b *Browser) object(e library.Entry, host string, prof profile.Profile, prefetch bool) any {
	if e.IsDir {
		return didl.Container{
			ID:         objectID(e.Path),
			ParentID:   parentID(e.Path),
			Restricted: 1,
			Searchable: 1,
			ChildCount: b.Lib.ChildCount(e.Path),
			Title:      e.Name,
			Class:      didl.ClassFolder,
		}
	}
	base := b.Media.BaseURL(host)
	video := didl.Res{
		ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", e.MIME, media.ContentFeatures),
		Size:         e.Size,
		URL:          media.VideoURL(base, e.Path),
	}
	if info, err := b.Lib.Probe(e.Path); err == nil {
		video.Duration = didl.Duration(info.Duration)
		video.Bitrate = info.Bitrate / 8 // DIDL bitrate is bytes per second
		if info.Width > 0 {
			video.Resolution = strconv.Itoa(info.Width) + "x" + strconv.Itoa(info.Height)
		}
	}
	item := didl.Item{
		ID:         objectID(e.Path),
		ParentID:   parentID(e.Path),
		Restricted: 1,
		Title:      e.Name,
		Class:      didl.ClassVideo,
		Date:       didl.Date(e.ModTime),
		Res:        []didl.Res{video},
	}
	subs, err := b.Media.Subtitles(base, e.Path)
	if err != nil {
		b.Logger.Warn("listing subtitles failed", "path", e.Path, "error", err)
	} else if len(subs) > 0 {
		prof.AddSubtitles(&item, subs)
		if prefetch {
			plain := make([]library.Subtitle, len(subs))
			for i, s := range subs {
				plain[i] = s.Subtitle
			}
			b.Media.Extractor.Prefetch(e.Path, plain)
		}
	}
	return item
}
