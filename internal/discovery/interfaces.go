package discovery

import (
	"net"
	"regexp"
)

// Iface is a network interface usable for discovery with its IPv4 networks.
type Iface struct {
	If    net.Interface
	Addrs []*net.IPNet
}

// virtual adapters hidden by default (Hyper-V/WSL, Docker, VM, VPN/tunnels).
var virtualName = regexp.MustCompile(`(?i)(vethernet|wsl|docker|virtualbox|vbox|vmware|vmnet|hyper-v|^veth|^br-|utun|tailscale|zerotier|wireguard|^tun|^tap)`)

// UsableInterfaces lists up, multicast-capable interfaces with a routable IPv4
// address. Virtual adapters are skipped unless all is true. If nothing usable
// is found, loopback is returned so single-host testing still works.
func UsableInterfaces(all bool) []Iface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out, loop []Iface
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		addrs := ipv4Nets(ifi)
		if len(addrs) == 0 {
			continue
		}
		if ifi.Flags&net.FlagLoopback != 0 {
			loop = append(loop, Iface{If: ifi, Addrs: addrs})
			continue
		}
		if ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		if !all && virtualName.MatchString(ifi.Name) {
			continue
		}
		out = append(out, Iface{If: ifi, Addrs: addrs})
	}
	if len(out) == 0 {
		return loop
	}
	return out
}

func ipv4Nets(ifi net.Interface) []*net.IPNet {
	as, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range as {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := n.IP.To4()
		if ip4 == nil || ip4.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, &net.IPNet{IP: ip4, Mask: n.Mask})
	}
	return out
}
