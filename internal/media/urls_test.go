package media

import (
	"net/netip"
	"testing"
)

func TestBaseURL(t *testing.T) {
	for host, want := range map[string]string{
		"192.168.1.82:1338": "http://192.168.1.82:1339",
		"192.168.1.82":      "http://192.168.1.82:1339",
		"[fd00::5]:1338":    "http://[fd00::5]:1339",
		"bee.local:1338":    "http://bee.local:1339",
	} {
		if got := BaseURL(host, 1339); got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestReplaceLinkLocal(t *testing.T) {
	addrs := func(s ...string) []netip.Addr {
		var ips []netip.Addr
		for _, a := range s {
			ips = append(ips, netip.MustParseAddr(a))
		}
		return ips
	}
	ifaces := [][]netip.Addr{
		addrs("127.0.0.1", "::1"),
		addrs("fe80::1", "fd00::1", "192.168.1.82"),
		addrs("fe80::2", "2001:db8::2"),
		addrs("fe80::3"),
	}
	for in, want := range map[string]string{
		"fe80::1":     "192.168.1.82",
		"fe80::1%en0": "192.168.1.82",
		"fe80::2":     "2001:db8::2",
		"fe80::3":     "",
		"fe80::9":     "",
	} {
		got, ok := replaceLinkLocal(netip.MustParseAddr(in), ifaces)
		if ok != (want != "") || ok && got.String() != want {
			t.Errorf("replaceLinkLocal(%s) = %v, %v, want %q", in, got, ok, want)
		}
	}
}
