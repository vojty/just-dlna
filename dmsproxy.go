package main

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// allNets lets every client use dms's control endpoint, which rejects
// clients outside AllowedIpNets.
var allNets = []*net.IPNet{
	{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
	{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
}

// publicAddrListener is dms's loopback listener reporting the address of the
// public proxy listener, from which dms takes the port it announces over SSDP.
type publicAddrListener struct {
	net.Listener
	addr net.Addr
}

func (l publicAddrListener) Addr() net.Addr { return l.addr }

// dmsAllowed reports whether a dms endpoint is needed by DLNA clients. dms
// also serves /res, /subtitle and /icon, which read files by a path in the
// query and, for /subtitle and /icon, outside the media folder, and
// /debug/pprof. Videos and subtitles are served by the media server instead.
func dmsAllowed(p string) bool {
	switch {
	case path.Clean(p) != p:
		return false
	case p == "/rootDesc.xml", p == "/ctl", p == "/evt/ContentDirectory":
		return true
	case strings.HasPrefix(p, "/scpd/") && strings.HasSuffix(p, ".xml"):
		return true
	case strings.HasPrefix(p, "/deviceIcon/"):
		return true
	}
	return false
}

// newDMSProxy forwards the UPnP endpoints to dms listening at addr.
func newDMSProxy(addr string) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// dms builds the URLs in its responses from the Host header.
			r.Out.Host = r.In.Host
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !dmsAllowed(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
