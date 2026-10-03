// Package netif picks the network interfaces SSDP announcements are sent on.
//
// Announcing on every interface is harmful on some hosts: an OpenThread Border
// Router's wpan0 is a low-bandwidth 802.15.4 mesh where each NOTIFY splits into
// several radio frames, and a burst of them every 30 seconds overflows the
// border router's queue and crashes Thread devices. Docker bridges and VPN
// tunnels have no DLNA clients either. Select keeps only interfaces that look
// like a LAN.
package netif

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Linux link types (ARPHRD_* in linux/if_arp.h) from /sys/class/net/<if>/type.
const (
	typeUnknown  = -1
	typeEther    = 1     // ARPHRD_ETHER: Ethernet, Wi-Fi, bridges, veth
	typeLoopback = 772   // ARPHRD_LOOPBACK
	typeIEEE15_4 = 804   // ARPHRD_IEEE802154: 802.15.4 radio (Thread, Zigbee)
	type6LoWPAN  = 825   // ARPHRD_6LOWPAN: IPv6 over 802.15.4
	typeNone     = 65534 // ARPHRD_NONE: tun devices, e.g. OTBR's wpan0, WireGuard
)

const (
	threadReason  = "Thread/802.15.4 mesh"
	dockerReason  = "Docker or container network"
	tunnelReason  = "VPN or tunnel"
	libvirtReason = "virtual machine bridge"
)

// namePrefixes lists interface names that are never a LAN DLNA clients are
// on. Most are also caught by their flags or link type; the names give a
// clearer reason in the log and cover Ethernet-type virtual interfaces
// (Docker bridges, veth pairs) that look like a LAN otherwise.
var namePrefixes = []struct{ prefix, reason string }{
	{"wpan", threadReason},
	{"lowpan", threadReason},
	{"docker", dockerReason},
	{"br-", dockerReason},
	{"veth", dockerReason},
	{"cni", dockerReason},
	{"flannel", dockerReason},
	{"virbr", libvirtReason},
	{"tun", tunnelReason},
	{"tap", tunnelReason},
	{"wg", tunnelReason},
	{"zt", tunnelReason},
	{"tailscale", tunnelReason},
}

// Interface is a network interface with its addresses and link type.
type Interface struct {
	net.Interface
	Addrs []netip.Addr
	Type  int // Linux ARPHRD_* link type, -1 when unknown (not Linux)
}

// Skipped is an interface left out of the selection.
type Skipped struct {
	Name   string
	Reason string
}

// List returns the interfaces of this host.
func List() ([]Interface, error) {
	ifis, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, len(ifis))
	for i, ifi := range ifis {
		out[i] = Interface{Interface: ifi, Addrs: addrs(ifi), Type: linkType(ifi.Name)}
	}
	return out, nil
}

func addrs(ifi net.Interface) []netip.Addr {
	as, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var ips []netip.Addr
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				ips = append(ips, ip.Unmap())
			}
		}
	}
	return ips
}

func linkType(name string) int {
	b, err := os.ReadFile("/sys/class/net/" + name + "/type")
	if err != nil {
		return typeUnknown
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return typeUnknown
	}
	return n
}

// ParseNames splits a comma-separated list of interface names, dropping
// blanks and duplicates.
func ParseNames(s string) []string {
	var names []string
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names
}

// Check returns why ifi is not suitable for SSDP, or "" when it is: it must be
// up, multicast-capable, not loopback or point-to-point, an Ethernet-type
// link (Wi-Fi is one too) not known to be virtual, with an IPv4 or a
// global/ULA IPv6 address.
func Check(ifi Interface) string {
	switch {
	case ifi.Flags&net.FlagUp == 0:
		return "down"
	case ifi.Flags&net.FlagLoopback != 0 || ifi.Type == typeLoopback:
		return "loopback"
	// The link type identifies a Thread radio whatever its name.
	case ifi.Type == typeIEEE15_4 || ifi.Type == type6LoWPAN:
		return threadReason
	}
	for _, p := range namePrefixes {
		if strings.HasPrefix(ifi.Name, p.prefix) {
			return p.reason
		}
	}
	switch {
	// OTBR's wpan0 is a tun device: point-to-point with no link layer. So are
	// WireGuard and most VPNs.
	case ifi.Flags&net.FlagPointToPoint != 0:
		return "point-to-point link (tunnel, VPN or Thread)"
	case ifi.Type == typeNone:
		return "no link layer (tunnel, VPN or Thread)"
	case ifi.Type != typeUnknown && ifi.Type != typeEther:
		return fmt.Sprintf("link type %d is not Ethernet or Wi-Fi", ifi.Type)
	case ifi.Flags&net.FlagMulticast == 0:
		return "no multicast"
	case !hasLANAddr(ifi.Addrs):
		return "no IPv4 or global/ULA IPv6 address"
	}
	return ""
}

func hasLANAddr(addrs []netip.Addr) bool {
	for _, a := range addrs {
		if a.Is4() && !a.IsLoopback() && !a.IsLinkLocalUnicast() && !a.IsUnspecified() {
			return true
		}
		// Includes ULA (fc00::/7).
		if a.Is6() && a.IsGlobalUnicast() {
			return true
		}
	}
	return false
}

// Select picks the interfaces to announce on. With names, it returns exactly
// those, in that order, and fails if one does not exist; Check is not applied
// so any interface can be chosen deliberately. Without names, it returns the
// interfaces passing Check. Either way, the rest are returned as skipped with
// the reason.
func Select(all []Interface, names []string) (chosen []Interface, skipped []Skipped, err error) {
	if len(names) > 0 {
		for _, n := range names {
			i := slices.IndexFunc(all, func(ifi Interface) bool { return ifi.Name == n })
			if i < 0 {
				return nil, nil, fmt.Errorf("interface %q not found", n)
			}
			chosen = append(chosen, all[i])
		}
		for _, ifi := range all {
			if !slices.Contains(names, ifi.Name) {
				skipped = append(skipped, Skipped{ifi.Name, "not listed in ifname"})
			}
		}
		return chosen, skipped, nil
	}
	for _, ifi := range all {
		if r := Check(ifi); r != "" {
			skipped = append(skipped, Skipped{ifi.Name, r})
		} else {
			chosen = append(chosen, ifi)
		}
	}
	return chosen, skipped, nil
}

// Names returns the names of ifs.
func Names(ifs []Interface) []string {
	names := make([]string, len(ifs))
	for i, ifi := range ifs {
		names[i] = ifi.Name
	}
	return names
}
