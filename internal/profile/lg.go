package profile

import (
	"net/http"
	"strings"

	"just-dlna/internal/didl"
)

// LG matches LG webOS / NetCast TVs. They pick up subtitles from additional
// <res> entries with a text/srt protocolInfo on the video item.
type LG struct{}

func (LG) Name() string { return "lg" }

func (LG) Match(ua string) bool {
	ua = strings.ToLower(ua)
	return strings.Contains(ua, "webos") ||
		strings.Contains(ua, "netcast") ||
		strings.Contains(ua, "lge") ||
		strings.Contains(ua, "lg-") ||
		strings.Contains(ua, "lgtv")
}

func (LG) AddSubtitles(item *didl.Item, subs []SubtitleRef) {
	for _, s := range subs {
		item.Res = append(item.Res, srtRes(s))
	}
}

func (LG) VideoHeaders(http.Header, *http.Request, []SubtitleRef) {}
