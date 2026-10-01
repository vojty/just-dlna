// Package media serves video files and subtitles over HTTP.
package media

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// BaseURL returns the media server base URL reachable at the same address the
// client used to reach the DLNA server (host may include a port).
func BaseURL(host string, port int) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func escapePath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// VideoURL is the URL of the original video file.
func VideoURL(base, rel string) string {
	return base + "/media/" + escapePath(rel)
}

// SubtitleURL is the URL of subtitle n of a video, always delivered as SRT.
func SubtitleURL(base, rel string, n int) string {
	return base + "/sub/" + strconv.Itoa(n) + "/" + escapePath(rel) + ".srt"
}
