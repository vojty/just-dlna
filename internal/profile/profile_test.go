package profile

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"just-dlna/internal/didl"
	"just-dlna/internal/library"
)

func TestForUserAgent(t *testing.T) {
	for ua, want := range map[string]string{
		"Linux/3.10.19-32.afro.4 UPnP/1.0 LGE WebOS TV LGE_DLNA_SDK/1.6.0": "lg",
		"webOS TV/Version 0.9":                    "lg",
		"SEC_HHP_[TV] Samsung Q7 Series (55)/1.0": "generic",
		"VLC/3.0.20 LibVLC/3.0.20":                "generic",
		"":                                        "generic",
	} {
		if got := ForUserAgent(ua).Name(); got != want {
			t.Errorf("ForUserAgent(%q) = %s, want %s", ua, got, want)
		}
	}
}

func TestLGSubtitleDIDL(t *testing.T) {
	item := didl.Item{
		ID: "a%2Fb.mkv", ParentID: "a", Restricted: 1, Title: "b.mkv", Class: didl.ClassVideo,
		Res: []didl.Res{{ProtocolInfo: "http-get:*:video/x-matroska:*", Size: 10, Duration: didl.Duration(90 * time.Minute), URL: "http://h/media/a/b.mkv"}},
	}
	LG{}.AddSubtitles(&item, []SubtitleRef{
		{Subtitle: library.Subtitle{Index: 0}, URL: "http://h/sub/0/a/b.mkv.srt"},
		{Subtitle: library.Subtitle{Index: 1}, URL: "http://h/sub/1/a/b.mkv.srt"},
	})
	out, err := xml.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	want := `<item id="a%2Fb.mkv" parentID="a" restricted="1"><dc:title>b.mkv</dc:title><upnp:class>object.item.videoItem</upnp:class>` +
		`<res protocolInfo="http-get:*:video/x-matroska:*" size="10" duration="1:30:00.000">http://h/media/a/b.mkv</res>` +
		`<res protocolInfo="http-get:*:text/srt:*">http://h/sub/0/a/b.mkv.srt</res>` +
		`<res protocolInfo="http-get:*:text/srt:*">http://h/sub/1/a/b.mkv.srt</res></item>`
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestGenericAddsCaptionInfo(t *testing.T) {
	var item didl.Item
	Generic{}.AddSubtitles(&item, []SubtitleRef{{URL: "http://h/sub/0/x&y.srt"}})
	if !strings.Contains(item.Extra, `sec:type="srt">http://h/sub/0/x&amp;y.srt</sec:CaptionInfoEx>`) {
		t.Errorf("missing CaptionInfoEx: %s", item.Extra)
	}
}
