package netif

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

const (
	lan  = net.FlagUp | net.FlagBroadcast | net.FlagMulticast | net.FlagRunning
	ptp  = net.FlagUp | net.FlagPointToPoint | net.FlagMulticast | net.FlagRunning
	loop = net.FlagUp | net.FlagLoopback | net.FlagRunning
)

func iface(name string, flags net.Flags, typ int, addrs ...string) Interface {
	ifi := Interface{Interface: net.Interface{Name: name, Flags: flags, MTU: 1500}, Type: typ}
	for _, a := range addrs {
		ifi.Addrs = append(ifi.Addrs, netip.MustParseAddr(a))
	}
	return ifi
}

// host is the server the Thread flood was seen on: Wi-Fi LAN, an OpenThread
// Border Router and Docker, with Linux link types.
var host = []Interface{
	iface("lo", loop, typeLoopback, "127.0.0.1", "::1"),
	iface("enp1s0", net.FlagBroadcast|net.FlagMulticast, typeEther),
	iface("wlo1", lan, typeEther, "192.168.1.82", "fe80::1", "fd00::82"),
	iface("wpan0", ptp, typeNone, "fd41::1", "fd09::1", "fd41::ff:fe00:8c00", "fe80::2"),
	iface("docker0", lan, typeEther, "172.17.0.1"),
	iface("br-1a2b3c", lan, typeEther, "172.18.0.1"),
	iface("veth12ab", lan, typeEther, "fe80::3"),
	iface("tailscale0", ptp, typeNone, "100.64.0.1"),
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		ifi  Interface
		want string // "" for chosen
	}{
		{iface("wlo1", lan, typeEther, "192.168.1.82"), ""},
		{iface("eth0", lan, typeEther, "fe80::1", "2001:db8::1"), ""},
		{iface("eth0", lan, typeEther, "fd00::1"), ""},
		// Not Linux: no link type.
		{iface("en0", lan, typeUnknown, "192.168.1.82"), ""},
		// A LAN bridge for VMs is fine, only Docker's br-<id> is skipped.
		{iface("br0", lan, typeEther, "192.168.1.2"), ""},

		{iface("eth0", net.FlagBroadcast|net.FlagMulticast, typeEther, "192.168.1.82"), "down"},
		{iface("lo", loop, typeLoopback, "127.0.0.1"), "loopback"},
		{iface("lo0", loop, typeUnknown, "127.0.0.1"), "loopback"},
		// OTBR's wpan0 (tun) and a native 802.15.4 radio, under any name.
		{iface("wpan0", ptp, typeNone, "fd41::1"), threadReason},
		{iface("thread0", ptp, typeNone, "fd41::1"), "point-to-point link (tunnel, VPN or Thread)"},
		{iface("radio0", lan, typeIEEE15_4, "fd41::1"), threadReason},
		{iface("radio0", lan, type6LoWPAN, "fd41::1"), threadReason},
		{iface("lowpan0", lan, typeEther, "fd41::1"), threadReason},
		{iface("gre9", lan, typeNone, "10.0.0.1"), "no link layer (tunnel, VPN or Thread)"},
		{iface("docker0", lan, typeEther, "172.17.0.1"), dockerReason},
		{iface("br-1a2b3c", lan, typeEther, "172.18.0.1"), dockerReason},
		{iface("veth12ab", lan, typeEther, "172.18.0.1"), dockerReason},
		{iface("virbr0", lan, typeEther, "192.168.122.1"), libvirtReason},
		{iface("tun0", lan, typeEther, "10.8.0.1"), tunnelReason},
		{iface("tap0", lan, typeEther, "10.8.0.1"), tunnelReason},
		{iface("wg0", ptp, typeNone, "10.9.0.1"), tunnelReason},
		{iface("zt3jnk", lan, typeEther, "10.147.17.1"), tunnelReason},
		{iface("tailscale0", ptp, typeNone, "100.64.0.1"), tunnelReason},
		{iface("utun3", ptp, typeUnknown, "fd7a::1"), "point-to-point link (tunnel, VPN or Thread)"},
		{iface("ib0", lan, 32, "10.0.0.1"), "link type 32 is not Ethernet or Wi-Fi"},
		{iface("eth0", net.FlagUp|net.FlagBroadcast, typeEther, "192.168.1.82"), "no multicast"},
		{iface("eth0", lan, typeEther), "no IPv4 or global/ULA IPv6 address"},
		{iface("awdl0", lan, typeUnknown, "fe80::1"), "no IPv4 or global/ULA IPv6 address"},
		{iface("eth0", lan, typeEther, "169.254.1.1"), "no IPv4 or global/ULA IPv6 address"},
	} {
		if got := Check(tc.ifi); got != tc.want {
			t.Errorf("Check(%s %v type %d %v) = %q, want %q", tc.ifi.Name, tc.ifi.Flags, tc.ifi.Type, tc.ifi.Addrs, got, tc.want)
		}
	}
}

func TestSelect(t *testing.T) {
	for _, tc := range []struct {
		names   []string
		chosen  []string
		skipped []string
		err     bool
	}{
		{nil, []string{"wlo1"}, []string{"lo", "enp1s0", "wpan0", "docker0", "br-1a2b3c", "veth12ab", "tailscale0"}, false},
		{[]string{"wlo1"}, []string{"wlo1"}, []string{"lo", "enp1s0", "wpan0", "docker0", "br-1a2b3c", "veth12ab", "tailscale0"}, false},
		// Listed interfaces are used as given, in that order, even if Check
		// would reject them.
		{[]string{"enp1s0", "wlo1", "docker0"}, []string{"enp1s0", "wlo1", "docker0"}, []string{"lo", "wpan0", "br-1a2b3c", "veth12ab", "tailscale0"}, false},
		{[]string{"wlo1", "eth9"}, nil, nil, true},
	} {
		chosen, skipped, err := Select(host, tc.names)
		if (err != nil) != tc.err {
			t.Errorf("Select(%q) error = %v", tc.names, err)
			continue
		}
		if got := Names(chosen); !slices.Equal(got, tc.chosen) {
			t.Errorf("Select(%q) chosen = %q, want %q", tc.names, got, tc.chosen)
		}
		var got []string
		for _, s := range skipped {
			if s.Reason == "" {
				t.Errorf("Select(%q) skipped %s without a reason", tc.names, s.Name)
			}
			got = append(got, s.Name)
		}
		if !slices.Equal(got, tc.skipped) {
			t.Errorf("Select(%q) skipped = %q, want %q", tc.names, got, tc.skipped)
		}
	}
}

func TestSelectNone(t *testing.T) {
	chosen, _, err := Select(host[3:4], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chosen) != 0 {
		t.Errorf("Select chose %q on a Thread-only host", Names(chosen))
	}
}

func TestParseNames(t *testing.T) {
	for in, want := range map[string][]string{
		"":                   nil,
		" , ":                nil,
		"eth0":               {"eth0"},
		"wlo1,enp1s0":        {"wlo1", "enp1s0"},
		" wlo1 , enp1s0 ,":   {"wlo1", "enp1s0"},
		"wlo1,wlo1,enp1s0":   {"wlo1", "enp1s0"},
		"wlo1,,enp1s0,wlo1 ": {"wlo1", "enp1s0"},
	} {
		if got := ParseNames(in); !slices.Equal(got, want) {
			t.Errorf("ParseNames(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestList(t *testing.T) {
	ifs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagLoopback != 0 && Check(ifi) != "loopback" {
			t.Errorf("loopback %s: Check = %q", ifi.Name, Check(ifi))
		}
	}
}
