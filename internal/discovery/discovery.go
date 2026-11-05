// Package discovery finds other netchat instances on the LAN with UDP
// multicast beacons (TTL 1). Replies never go over UDP: a receiver answers an
// announcing beacon over HTTP (see node), following LocalSend's design.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/net/ipv4"

	"netchat/internal/protocol"
)

const (
	BeaconInterval  = 30 * time.Second
	ifaceRefresh    = 5 * time.Second
	maxBeaconBytes  = 8 << 10
	maxReadFailures = 10 // Windows surfaces ICMP errors from earlier sends on reads
)

var startupBurst = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

type Discovery struct {
	Port      int
	Group     net.IP
	AllIfaces bool
	Self      func(announce bool) protocol.Info
	SelfID    string
	OnBeacon  func(info protocol.Info, ip net.IP)
	// OnNetworkChange is called (from the send loop) after a new interface or
	// address appeared, e.g. Wi-Fi turned on after startup. Optional.
	OnNetworkChange func()
	Log             *slog.Logger

	pc     *ipv4.PacketConn
	joined map[int]joinedIface
	wake   chan bool // request an extra beacon (value = announce)
}

type joinedIface struct {
	If    net.Interface
	addrs string // IPv4 addresses when joined; a change means a new network
}

// Announce sends an extra beacon soon (e.g. after identities changed).
func (d *Discovery) Announce(announce bool) {
	select {
	case d.wake <- announce:
	default:
	}
}

// Run blocks until ctx is cancelled.
func (d *Discovery) Run(ctx context.Context) error {
	d.wake = make(chan bool, 1)
	d.joined = map[int]joinedIface{}
	lc := net.ListenConfig{Control: reuseControl}
	c, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", d.Port))
	if err != nil {
		return fmt.Errorf("discovery listen udp %d: %w", d.Port, err)
	}
	defer c.Close()
	d.pc = ipv4.NewPacketConn(c)
	_ = d.pc.SetMulticastTTL(1)
	_ = d.pc.SetMulticastLoopback(true)
	d.refreshInterfaces()

	go func() { <-ctx.Done(); c.Close() }()
	go d.sendLoop(ctx)
	return d.readLoop(ctx)
}

// refreshInterfaces syncs group membership with the usable interfaces and
// reports whether a new interface (or a known one with new addresses) was joined.
func (d *Discovery) refreshInterfaces() (added bool) {
	group := &net.UDPAddr{IP: d.Group}
	seen := map[int]bool{}
	for _, it := range UsableInterfaces(d.AllIfaces) {
		addrs := fmt.Sprint(it.Addrs)
		if j, ok := d.joined[it.If.Index]; ok {
			if j.addrs == addrs {
				seen[it.If.Index] = true
				continue
			}
			// Same adapter, new network (reconnect/DHCP): the OS may have
			// dropped our membership, so rejoin.
			_ = d.pc.LeaveGroup(&j.If, group)
			delete(d.joined, it.If.Index)
		}
		ifi := it.If
		if err := d.pc.JoinGroup(&ifi, group); err != nil {
			d.Log.Debug("join multicast group failed", "iface", ifi.Name, "err", err)
			continue
		}
		seen[ifi.Index] = true
		d.joined[ifi.Index] = joinedIface{If: ifi, addrs: addrs}
		added = true
		d.Log.Info("discovery on interface", "iface", ifi.Name, "addrs", addrs)
	}
	for idx, j := range d.joined {
		if !seen[idx] {
			_ = d.pc.LeaveGroup(&j.If, group)
			delete(d.joined, idx)
			d.Log.Info("interface gone", "iface", j.If.Name)
		}
	}
	return added
}

// sendLoop owns interface membership and SetMulticastInterface (not goroutine safe).
func (d *Discovery) sendLoop(ctx context.Context) {
	for _, wait := range startupBurst {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		d.send(true)
	}
	beacon := time.NewTicker(BeaconInterval)
	refresh := time.NewTicker(ifaceRefresh)
	defer beacon.Stop()
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			// Compare membership, not counts: Wi-Fi coming up replaces the
			// loopback fallback, so the count stays the same.
			if d.refreshInterfaces() {
				d.send(true) // new network: ask everyone to register with us
				if d.OnNetworkChange != nil {
					d.OnNetworkChange()
				}
			}
		case <-beacon.C:
			d.send(false)
		case a := <-d.wake:
			d.send(a)
		}
	}
}

func (d *Discovery) send(announce bool) {
	b, err := json.Marshal(d.Self(announce))
	if err != nil {
		d.Log.Error("marshal beacon", "err", err)
		return
	}
	dst := &net.UDPAddr{IP: d.Group, Port: d.Port}
	for _, j := range d.joined {
		ifi := j.If
		if err := d.pc.SetMulticastInterface(&ifi); err != nil {
			d.Log.Debug("set multicast iface", "iface", ifi.Name, "err", err)
			continue
		}
		if _, err := d.pc.WriteTo(b, nil, dst); err != nil {
			d.Log.Debug("send beacon", "iface", ifi.Name, "err", err)
		}
	}
}

func (d *Discovery) readLoop(ctx context.Context) error {
	buf := make([]byte, maxBeaconBytes)
	failures := 0
	for {
		n, _, src, err := d.pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			failures++
			if failures > maxReadFailures {
				return fmt.Errorf("discovery read: %w", err)
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		failures = 0
		ua, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		var info protocol.Info
		if err := json.Unmarshal(buf[:n], &info); err != nil || info.Proto != protocol.Proto {
			continue
		}
		if info.InstanceID == "" || info.InstanceID == d.SelfID || info.Port <= 0 {
			continue
		}
		d.OnBeacon(info, ua.IP)
	}
}
