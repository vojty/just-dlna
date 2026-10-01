// Package profile holds client-specific behaviour. Each DLNA client (LG,
// Samsung, ...) finds subtitles differently; a Profile adapts the DIDL item and
// the HTTP response headers to what a given client expects.
//
// To support a new client: implement Profile in a new file and add it to
// Registry before Generic.
package profile

import (
	"net/http"

	"just-dlna/internal/didl"
	"just-dlna/internal/library"
)

// SubtitleRef is a subtitle as offered to clients. The URL always serves SRT.
type SubtitleRef struct {
	library.Subtitle
	URL string
}

// Profile customises DIDL output and HTTP headers for one family of clients.
type Profile interface {
	Name() string
	// Match reports whether the profile applies to a client User-Agent.
	Match(userAgent string) bool
	// AddSubtitles adds subtitle information to a video item.
	AddSubtitles(item *didl.Item, subs []SubtitleRef)
	// VideoHeaders sets extra response headers when the video is served.
	VideoHeaders(h http.Header, r *http.Request, subs []SubtitleRef)
}

// Registry lists profiles in match order; Generic matches everything and must be last.
var Registry = []Profile{
	LG{},
	Generic{},
}

// ForUserAgent returns the first profile matching userAgent.
func ForUserAgent(userAgent string) Profile {
	for _, p := range Registry {
		if p.Match(userAgent) {
			return p
		}
	}
	return Generic{}
}

// srtRes returns a <res> entry pointing at an SRT subtitle.
func srtRes(s SubtitleRef) didl.Res {
	return didl.Res{ProtocolInfo: "http-get:*:text/srt:*", URL: s.URL}
}
