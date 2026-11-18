// Package node is the netchat daemon core: it owns the peer table and runs
// discovery, peer contact, gossip, liveness and subnet-scan loops.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"netchat/internal/bus"
	"netchat/internal/config"
	"netchat/internal/discovery"
	"netchat/internal/protocol"
	"netchat/internal/store"
)

const (
	contactTimeout = 3 * time.Second
	scanTimeout    = 600 * time.Millisecond
	scanParallel   = 50
	scanGrace      = 8 * time.Second
	livenessEvery  = 15 * time.Second
	staleAfter     = 45 * time.Second
	gossipEvery    = 60 * time.Second
	pruneEvery     = 30 * time.Second
	maxBody        = 1 << 20
)

var ErrSelf = errors.New("address points to this instance")

type Node struct {
	Cfg   config.Config
	Peers *PeerTable
	Log   *slog.Logger
	Store *store.Store
	Bus   *bus.Bus

	disc   *discovery.Discovery
	client *http.Client

	favMu     sync.Mutex
	favorites []string

	scanMu   sync.Mutex
	scanning bool

	idMu   sync.RWMutex
	idents []protocol.Identity
	idRev  int64

	boardSeq atomic.Int64
	runCtx   atomic.Pointer[context.Context] // set by Run; for background work started from handlers

	pres *presence

	syncMu sync.Mutex
	syncs  map[string]bool // peers with a running board subscription
}

func New(cfg config.Config, log *slog.Logger, st *store.Store) (*Node, error) {
	n := &Node{
		Cfg:    cfg,
		Peers:  NewPeerTable(),
		Log:    log,
		Store:  st,
		Bus:    bus.New(),
		client: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}},
		syncs:  map[string]bool{},
		pres:   newPresence(),
	}
	n.loadFavorites()
	if err := n.loadIdentities(); err != nil {
		return nil, err
	}
	seq, err := st.MaxSeq(protocol.BoardChannel(cfg.InstanceID))
	if err != nil {
		return nil, err
	}
	n.boardSeq.Store(seq)
	return n, nil
}

// SelfInfo is this instance's public description.
func (n *Node) SelfInfo(announce bool) protocol.Info {
	refs := n.localRefs() // with presence
	n.idMu.RLock()
	rev := n.idRev
	n.idMu.RUnlock()
	return protocol.Info{
		Proto:         protocol.Proto,
		Version:       protocol.Version,
		InstanceID:    n.Cfg.InstanceID,
		Machine:       n.Cfg.Name,
		Port:          n.Cfg.HTTPPort,
		Identities:    refs,
		IdentitiesRev: rev,
		BoardSeq:      n.boardSeq.Load(),
		Announce:      announce,
	}
}

// Run starts all background loops; it blocks until ctx is done or discovery fails.
func (n *Node) Run(ctx context.Context) error {
	group := net.ParseIP(n.Cfg.Group).To4()
	if group == nil || !group.IsMulticast() {
		return fmt.Errorf("invalid multicast group %q", n.Cfg.Group)
	}
	n.runCtx.Store(&ctx)
	n.disc = &discovery.Discovery{
		Port:       n.Cfg.Port,
		LegacyPort: protocol.LegacyPort,
		Group:      group,
		AllIfaces:  n.Cfg.AllIfaces,
		Self:       n.SelfInfo,
		SelfID:     n.Cfg.InstanceID,
		OnBeacon:   n.onBeacon,
		Log:        n.Log,
		OnNetworkChange: func() {
			n.Log.Info("network changed, looking for peers")
			go n.findPeers(ctx)
		},
	}
	go n.every(ctx, livenessEvery, n.checkLiveness)
	go n.every(ctx, gossipEvery, n.gossip)
	go n.every(ctx, syncEvery, n.reconcileBoardSyncs)
	go n.every(ctx, presenceEvery, func(context.Context) { n.checkPresence() })
	go n.every(ctx, pruneEvery, func(context.Context) {
		for _, name := range n.Peers.Prune() {
			n.Log.Info("peer forgotten", "machine", name)
		}
	})
	go n.findPeers(ctx)
	return n.disc.Run(ctx)
}

// findPeers contacts favorites and, if multicast finds nobody within the
// grace period, scans the local subnets. Run at startup and on network change.
func (n *Node) findPeers(ctx context.Context) {
	n.contactFavorites(ctx)
	select {
	case <-ctx.Done():
		return
	case <-time.After(scanGrace):
	}
	now := time.Now()
	for _, p := range n.Peers.Snapshot() {
		if p.Online(now) {
			return
		}
	}
	n.Log.Info("no peers via multicast yet, scanning local subnets")
	n.Scan(ctx)
}

func (n *Node) every(ctx context.Context, d time.Duration, f func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f(ctx)
		}
	}
}

func (n *Node) onBeacon(info protocol.Info, ip net.IP) {
	n.peerSeen(info, ip.String(), "beacon")
	if info.Announce {
		// Answer over HTTP, never UDP: lets the newcomer learn us immediately.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), contactTimeout)
			defer cancel()
			_, _ = n.Contact(ctx, net.JoinHostPort(ip.String(), strconv.Itoa(info.Port)), "beacon")
		}()
	}
}

// Register records a peer that contacted us via POST /p/register.
func (n *Node) Register(info protocol.Info, host string) {
	n.peerSeen(info, host, "register")
}

// Contact exchanges Info with the instance at addr (POST /p/register) and
// records it. Both sides learn about each other in one round trip.
func (n *Node) Contact(ctx context.Context, addr, via string) (protocol.Info, error) {
	var info protocol.Info
	body, _ := json.Marshal(n.SelfInfo(false))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/p/register", bytes.NewReader(body))
	if err != nil {
		return info, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return info, ErrSelf
	}
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("%s: HTTP %d", addr, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&info); err != nil {
		return info, fmt.Errorf("%s: bad response: %w", addr, err)
	}
	if info.Proto != protocol.Proto || info.InstanceID == "" {
		return info, fmt.Errorf("%s: not a netchat instance", addr)
	}
	if info.InstanceID == n.Cfg.InstanceID {
		return info, ErrSelf
	}
	host, _, _ := net.SplitHostPort(addr)
	n.peerSeen(info, host, via)
	return info, nil
}

// checkLiveness re-contacts peers we have not heard from recently (e.g. peers
// on other subnets that never reach us with multicast beacons).
func (n *Node) checkLiveness(ctx context.Context) {
	now := time.Now()
	var wg sync.WaitGroup
	for _, p := range n.Peers.Snapshot() {
		if now.Sub(p.LastSeen) < staleAfter {
			continue
		}
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, contactTimeout)
			defer cancel()
			_, _ = n.Contact(c, addr, "liveness")
		}(p.Addr())
	}
	wg.Wait()
	n.contactFavorites(ctx)
}

// peerSeen records a sighting. A new (or returning) peer is asked for its
// peer list right away, so the whole network is known within seconds.
func (n *Node) peerSeen(info protocol.Info, host, via string) {
	if !n.Peers.Upsert(info, host, via) {
		return
	}
	n.Log.Info("peer online", "machine", info.Machine, "addr", net.JoinHostPort(host, strconv.Itoa(info.Port)), "via", via)
	if ctx := n.runCtx.Load(); ctx != nil {
		if p, ok := n.Peers.Get(info.InstanceID); ok {
			go n.gossipFrom(*ctx, p)
		}
	}
}

// gossip asks a random online peer for its peer list and contacts unknown ones.
func (n *Node) gossip(ctx context.Context) {
	now := time.Now()
	var online []Peer
	for _, p := range n.Peers.Snapshot() {
		if p.Online(now) {
			online = append(online, p)
		}
	}
	if len(online) == 0 {
		return
	}
	n.gossipFrom(ctx, online[rand.IntN(len(online))])
}

func (n *Node) gossipFrom(ctx context.Context, src Peer) {
	now := time.Now()
	c, cancel := context.WithTimeout(ctx, contactTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, http.MethodGet, "http://"+src.Addr()+"/p/peers", nil)
	resp, err := n.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var entries []protocol.PeerEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&entries); err != nil {
		return
	}
	for _, e := range entries {
		if e.InstanceID == n.Cfg.InstanceID {
			continue
		}
		if p, ok := n.Peers.Get(e.InstanceID); ok && p.Online(now) {
			continue
		}
		// A loopback address means "on the reporting peer's host".
		if h, port, err := net.SplitHostPort(e.Addr); err == nil && isLoopback(h) {
			e.Addr = net.JoinHostPort(src.Host, port)
		}
		// Gossip is only a candidate: verify by contacting directly.
		go func(addr string) {
			c, cancel := context.WithTimeout(ctx, contactTimeout)
			defer cancel()
			_, _ = n.Contact(c, addr, "gossip")
		}(e.Addr)
	}
}

// PeerEntries is our answer to GET /p/peers.
func (n *Node) PeerEntries() []protocol.PeerEntry {
	now := time.Now()
	var out []protocol.PeerEntry
	for _, p := range n.Peers.Snapshot() {
		if p.Online(now) {
			out = append(out, protocol.PeerEntry{InstanceID: p.Info.InstanceID, Machine: p.Info.Machine, Addr: p.Addr()})
		}
	}
	return out
}

// Scan probes every host in the /24 around each local IPv4 address.
// It returns the number of instances found. Only one scan runs at a time.
func (n *Node) Scan(ctx context.Context) int {
	n.scanMu.Lock()
	if n.scanning {
		n.scanMu.Unlock()
		return 0
	}
	n.scanning = true
	n.scanMu.Unlock()
	defer func() { n.scanMu.Lock(); n.scanning = false; n.scanMu.Unlock() }()

	own := map[string]bool{}
	var targets []string
	seenNet := map[string]bool{}
	ports := []string{strconv.Itoa(n.Cfg.Port)}
	if n.Cfg.Port != protocol.LegacyPort {
		ports = append(ports, strconv.Itoa(protocol.LegacyPort)) // older versions
	}
	ifs := discovery.UsableInterfaces(n.Cfg.AllIfaces)
	for _, it := range ifs {
		for _, a := range it.Addrs {
			own[a.IP.String()] = true
		}
	}
	for _, it := range ifs {
		if it.If.Flags&net.FlagLoopback != 0 {
			continue
		}
		for _, a := range it.Addrs {
			base := a.IP.Mask(net.CIDRMask(24, 32)).To4()
			if seenNet[base.String()] {
				continue
			}
			seenNet[base.String()] = true
			for i := 1; i < 255; i++ {
				ip := net.IPv4(base[0], base[1], base[2], byte(i)).String()
				if !own[ip] {
					for _, p := range ports {
						targets = append(targets, net.JoinHostPort(ip, p))
					}
				}
			}
		}
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		found = map[string]bool{} // instance IDs: a peer may answer on both ports
		sem   = make(chan struct{}, scanParallel)
	)
	for _, addr := range targets {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, scanTimeout)
			defer cancel()
			if info, err := n.Contact(c, addr, "scan"); err == nil {
				mu.Lock()
				found[info.InstanceID] = true
				mu.Unlock()
			}
		}(addr)
	}
	wg.Wait()
	n.Log.Info("subnet scan done", "probed", len(targets), "found", len(found))
	return len(found)
}

// AddPeer contacts addr ("host" or "host:port") and saves it as a favorite.
func (n *Node) AddPeer(ctx context.Context, addr string) (protocol.Info, error) {
	addr = strings.TrimSpace(addr)
	addrs := []string{addr}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// No port: try ours, then the one older versions listen on.
		addrs = []string{net.JoinHostPort(addr, strconv.Itoa(protocol.DefaultPort)),
			net.JoinHostPort(addr, strconv.Itoa(protocol.LegacyPort))}
	}
	var (
		info     protocol.Info
		err      error
		firstErr error
	)
	for _, addr = range addrs {
		c, cancel := context.WithTimeout(ctx, contactTimeout)
		info, err = n.Contact(c, addr, "manual")
		cancel()
		if err == nil || errors.Is(err, ErrSelf) {
			break
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if errors.Is(err, ErrSelf) {
		return info, err
	}
	if err != nil {
		return info, firstErr
	}
	n.favMu.Lock()
	known := false
	for _, f := range n.favorites {
		if f == addr {
			known = true
		}
	}
	if !known {
		n.favorites = append(n.favorites, addr)
	}
	n.favMu.Unlock()
	if !known {
		n.saveFavorites()
	}
	return info, nil
}

func (n *Node) contactFavorites(ctx context.Context) {
	n.favMu.Lock()
	favs := append([]string(nil), n.favorites...)
	n.favMu.Unlock()
	for _, addr := range favs {
		go func(addr string) {
			c, cancel := context.WithTimeout(ctx, contactTimeout)
			defer cancel()
			_, _ = n.Contact(c, addr, "manual")
		}(addr)
	}
}

func (n *Node) favoritesPath() string { return filepath.Join(n.Cfg.DataDir, "favorites.json") }

func (n *Node) loadFavorites() {
	b, err := os.ReadFile(n.favoritesPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &n.favorites)
}

func (n *Node) saveFavorites() {
	n.favMu.Lock()
	b, _ := json.MarshalIndent(n.favorites, "", "  ")
	n.favMu.Unlock()
	if err := os.WriteFile(n.favoritesPath(), b, 0o644); err != nil {
		n.Log.Warn("save favorites", "err", err)
	}
}

// Machines is the control-API list: self first, then peers.
func (n *Node) Machines() []protocol.Machine {
	now := time.Now()
	self := n.SelfInfo(false)
	out := []protocol.Machine{{
		InstanceID: self.InstanceID, Name: self.Machine, Self: true, Online: true,
		LastSeen: now, Version: self.Version, Identities: self.Identities,
	}}
	for _, p := range n.Peers.Snapshot() {
		out = append(out, protocol.Machine{
			InstanceID: p.Info.InstanceID, Name: p.Info.Machine, Addr: p.Addr(),
			Online: p.Online(now), LastSeen: p.LastSeen, Via: p.Via,
			Version: p.Info.Version, Identities: p.Info.Identities,
		})
	}
	return out
}
