package osutil

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// WSL describes whether we run inside WSL and how its network is set up.
type WSL struct {
	IsWSL bool
	Mode  string // "nat", "mirrored", or "" (unknown / not WSL)
}

// DetectWSL reports WSL and its networking mode. WSL2 defaults to NAT: Linux
// sits on a private virtual network behind Windows, so LAN multicast and
// inbound LAN connections never reach it.
func DetectWSL() WSL {
	if !IsWSL() {
		return WSL{}
	}
	w := WSL{IsWSL: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "wslinfo", "--networking-mode").Output(); err == nil {
		w.Mode = strings.ToLower(strings.TrimSpace(string(out)))
		return w
	}
	// Older WSL without wslinfo: NAT gives only private 172.16/12 addresses.
	if onlyWSLNatAddrs() {
		w.Mode = "nat"
	}
	return w
}

// IsWSL reports whether we run inside Windows Subsystem for Linux.
func IsWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func onlyWSLNatAddrs() bool {
	_, wslNet, _ := net.ParseCIDR("172.16.0.0/12")
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	seen := false
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil || n.IP.IsLoopback() {
			continue
		}
		if !wslNet.Contains(n.IP) {
			return false
		}
		seen = true
	}
	return seen
}

// WSLAdvice returns warning lines for a problematic WSL setup, or nil.
func WSLAdvice(w WSL, ips []string) []string {
	if !w.IsWSL {
		return nil
	}
	if w.Mode == "mirrored" {
		return []string{
			"Running in WSL2 (mirrored networking): Windows and WSL share one IP.",
			"If netchat also runs on Windows, use other ports here: --http-port 47810 --api 127.0.0.1:47811",
			"If LAN machines cannot reach this instance, allow inbound for WSL (admin PowerShell):",
			"  Set-NetFirewallHyperVVMSetting -Name '{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}' -DefaultInboundAction Allow",
		}
	}
	addr := "<this-wsl-ip>"
	if len(ips) > 0 {
		addr = ips[0]
	}
	return []string{
		"Running in WSL2 with NAT networking: this instance is on a private virtual network",
		"behind Windows. Machines on your LAN cannot see it and it cannot discover them.",
		"Fix: in %UserProfile%\\.wslconfig on Windows add",
		"       [wsl2]",
		"       networkingMode=mirrored",
		"     then run `wsl --shutdown` and start again (if netchat also runs on Windows:",
		"     --http-port 47810 --api 127.0.0.1:47811).",
		"Or link just this PC: on Windows run `netchat add " + addr + "` (the WSL IP can change after restarts).",
	}
}
