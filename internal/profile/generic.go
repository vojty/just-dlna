package profile

import (
	"net/http"

	"just-dlna/internal/didl"
)

// Generic is the fallback for unknown clients. It emits the common subtitle
// hints: text/srt <res> entries plus Samsung-style sec:CaptionInfoEx and the
// CaptionInfo.sec header. Clients ignore what they don't understand.
type Generic struct{}

func (Generic) Name() string { return "generic" }

func (Generic) Match(string) bool { return true }

func (Generic) AddSubtitles(item *didl.Item, subs []SubtitleRef) {
	for _, s := range subs {
		item.Res = append(item.Res, srtRes(s))
	}
	if len(subs) > 0 {
		item.Extra += `<sec:CaptionInfoEx xmlns:sec="http://www.sec.co.kr/" sec:type="srt">` +
			didl.Escape(subs[0].URL) + `</sec:CaptionInfoEx>`
	}
}

func (Generic) VideoHeaders(h http.Header, r *http.Request, subs []SubtitleRef) {
	if len(subs) > 0 && r.Header.Get("getCaptionInfo.sec") == "1" {
		h.Set("CaptionInfo.sec", subs[0].URL)
	}
}
