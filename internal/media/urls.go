// Package media serves video files and subtitles over HTTP.
package media

import (
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// BaseURL returns the media server base URL reachable at the same address the
// client used to reach the DLNA server (host may include a port).
func (s *Server) BaseURL(host string) string {
	return baseURL(host, s.Port, s.Interfaces)
}

// baseURL is BaseURL for the media server on port, replacing link-local
// addresses only with addresses of the allowed interfaces (nil: all).
func baseURL(host string, port int, allowed []string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is6() && ip.IsLinkLocalUnicast() {
		if alt, ok := replaceLinkLocal(ip, interfaceAddrs(allowed)); ok {
			host = alt.String()
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// replaceLinkLocal picks another address of the interface that owns the IPv6
// link-local address ip: IPv4 first, then a non-link-local IPv6. Clients
// discovering the server over IPv6 use its link-local address, which is only
// usable together with a zone (fe80::1%en0) that is the client's own interface
// name and so cannot be put into URLs; VLC, for one, fails to open them.
func replaceLinkLocal(ip netip.Addr, ifaces [][]netip.Addr) (netip.Addr, bool) {
	ip = ip.WithZone("")
	for _, addrs := range ifaces {
		owner := false
		var v4, v6 netip.Addr
		for _, a := range addrs {
			switch {
			case a == ip:
				owner = true
			case a.Is4() && !v4.IsValid():
				v4 = a
			case a.Is6() && !a.IsLinkLocalUnicast() && !v6.IsValid():
				v6 = a
			}
		}
		if !owner {
			continue
		}
		if v4.IsValid() {
			return v4, true
		}
		return v6, v6.IsValid()
	}
	return netip.Addr{}, false
}

// interfaceAddrs lists the unicast addresses of each up network interface in
// allowed, or of all of them for nil. SSDP announces only on the allowed
// interfaces, so addresses of others (a Thread mesh, Docker bridges) must not
// end up in URLs either.
func interfaceAddrs(allowed []string) [][]netip.Addr {
	ifis, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var all [][]netip.Addr
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 || allowed != nil && !slices.Contains(allowed, ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		var ips []netip.Addr
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(n.IP); ok {
					ips = append(ips, ip.Unmap())
				}
			}
		}
		all = append(all, ips)
	}
	return all
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
