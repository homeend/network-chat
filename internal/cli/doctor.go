package cli

import (
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"netchat/internal/config"
	"netchat/internal/discovery"
	"netchat/internal/osutil"
	"netchat/internal/protocol"
)

// doctor prints what netchat sees of the network and common setup problems.
// It is read-only and works whether or not the service runs.
func doctor(args []string) error {
	o, err := parseServe("doctor", args)
	if err != nil {
		return err
	}
	cfg := o.cfg
	fmt.Printf("netchat %s on %s/%s\n", protocol.Version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("machine name:  %s\n", cfg.Name)
	fmt.Printf("data dir:      %s\n", cfg.DataDir)
	fmt.Printf("LAN port:      %d (UDP beacons to %s, HTTP %d)\n", cfg.Port, cfg.Group, cfg.HTTPPort)

	c := http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Get("http://" + cfg.APIAddr + "/v1/machines"); err == nil {
		resp.Body.Close()
		fmt.Printf("service:       running, UI at http://%s/\n", cfg.APIAddr)
	} else {
		fmt.Printf("service:       not running at %s\n", cfg.APIAddr)
	}

	ifs := discovery.UsableInterfaces(cfg.AllIfaces)
	fmt.Println("discovery on:")
	if len(ifs) == 0 {
		fmt.Println("  (no usable network interface)")
	}
	for _, it := range ifs {
		var as []string
		for _, a := range it.Addrs {
			as = append(as, a.String())
		}
		fmt.Printf("  %-24s %s\n", it.If.Name, strings.Join(as, ", "))
	}

	var warnings []string
	warnings = append(warnings, osutil.WSLAdvice(osutil.DetectWSL(), localIPs(cfg))...)
	if len(ifs) == 1 && ifs[0].If.Flags&net.FlagLoopback != 0 {
		warnings = append(warnings, "Only loopback is usable: other machines cannot reach this one.")
	}
	if runtime.GOOS == "windows" {
		warnings = append(warnings, "Windows: the network profile must be Private (Settings > Network > Properties)",
			"and netchat must be allowed in the firewall prompt, or other machines cannot connect.")
	}
	if len(warnings) > 0 {
		fmt.Println()
		for _, w := range warnings {
			fmt.Println("! " + w)
		}
	}
	return nil
}

// localIPs lists this machine's non-loopback discovery IPv4 addresses.
func localIPs(cfg config.Config) []string {
	var out []string
	for _, it := range discovery.UsableInterfaces(cfg.AllIfaces) {
		if it.If.Flags&net.FlagLoopback != 0 {
			continue
		}
		for _, a := range it.Addrs {
			out = append(out, a.IP.String())
		}
	}
	return out
}
