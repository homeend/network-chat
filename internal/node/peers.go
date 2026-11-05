package node

import (
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"netchat/internal/protocol"
)

const (
	OfflineAfter = 90 * time.Second
	ForgetAfter  = 10 * time.Minute
)

// Peer is a known remote instance.
type Peer struct {
	Info      protocol.Info
	Host      string // address as seen by us (packet source / dialed host)
	Via       string
	FirstSeen time.Time
	LastSeen  time.Time
}

func (p Peer) Addr() string { return net.JoinHostPort(p.Host, strconv.Itoa(p.Info.Port)) }

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p Peer) Online(now time.Time) bool { return now.Sub(p.LastSeen) <= OfflineAfter }

type PeerTable struct {
	mu sync.Mutex
	m  map[string]*Peer
}

func NewPeerTable() *PeerTable { return &PeerTable{m: map[string]*Peer{}} }

// Upsert records that we heard from info.InstanceID at host. It returns true
// when the peer is new or came back after being offline.
func (t *PeerTable) Upsert(info protocol.Info, host, via string) bool {
	info.Announce = false
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.m[info.InstanceID]
	if !ok {
		p = &Peer{FirstSeen: now, Via: via}
		t.m[info.InstanceID] = p
	}
	back := ok && !p.Online(now)
	// Keep a LAN-reachable address; a loopback one is useless to other machines.
	if !(ok && isLoopback(host) && !isLoopback(p.Host)) {
		p.Host = host
	}
	p.Info, p.LastSeen = info, now
	return !ok || back
}

func (t *PeerTable) Get(id string) (Peer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.m[id]
	if !ok {
		return Peer{}, false
	}
	return *p, true
}

// Snapshot returns copies sorted by machine name.
func (t *PeerTable) Snapshot() []Peer {
	t.mu.Lock()
	out := make([]Peer, 0, len(t.m))
	for _, p := range t.m {
		out = append(out, *p)
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Info.Machine != out[j].Info.Machine {
			return out[i].Info.Machine < out[j].Info.Machine
		}
		return out[i].Info.InstanceID < out[j].Info.InstanceID
	})
	return out
}

// Prune forgets peers not heard from for ForgetAfter; returns their names.
func (t *PeerTable) Prune() []string {
	now := time.Now()
	var gone []string
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, p := range t.m {
		if now.Sub(p.LastSeen) > ForgetAfter {
			gone = append(gone, p.Info.Machine)
			delete(t.m, id)
		}
	}
	return gone
}
