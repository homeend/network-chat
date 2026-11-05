package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"netchat/internal/protocol"
	"netchat/internal/sse"
)

const (
	syncEvery    = 2 * time.Second
	syncRetry    = 3 * time.Second
	BoardBacklog = 200
)

// reconcileBoardSyncs keeps one SSE subscription to every online peer's
// machine board, so all boards are cached locally and update live.
func (n *Node) reconcileBoardSyncs(ctx context.Context) {
	now := time.Now()
	for _, p := range n.Peers.Snapshot() {
		if !p.Online(now) {
			continue
		}
		id := p.Info.InstanceID
		n.syncMu.Lock()
		running := n.syncs[id]
		if !running {
			n.syncs[id] = true
		}
		n.syncMu.Unlock()
		if !running {
			go n.syncBoard(ctx, id)
		}
	}
}

func (n *Node) syncBoard(ctx context.Context, peerID string) {
	defer func() {
		n.syncMu.Lock()
		delete(n.syncs, peerID)
		n.syncMu.Unlock()
	}()
	channel := protocol.BoardChannel(peerID)
	for ctx.Err() == nil {
		p, ok := n.Peers.Get(peerID)
		if !ok || !p.Online(time.Now()) {
			return
		}
		err := n.streamBoard(ctx, p, channel)
		if ctx.Err() != nil {
			return
		}
		n.Log.Debug("board subscription ended", "machine", p.Info.Machine, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(syncRetry):
		}
	}
}

func (n *Node) streamBoard(ctx context.Context, p Peer, channel string) error {
	after, err := n.Store.MaxSeq(channel)
	if err != nil {
		return err
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, "http://"+p.Addr()+"/p/board/events?after="+strconv.FormatInt(after, 10), nil)
	if err != nil {
		return err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Stop if the peer goes quiet (pings every 15s) or offline.
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-c.Done():
				return
			case <-alive:
				last = time.Now()
			case <-t.C:
				pp, ok := n.Peers.Get(p.Info.InstanceID)
				if time.Since(last) > 45*time.Second || !ok || !pp.Online(time.Now()) || pp.Addr() != p.Addr() {
					cancel()
					return
				}
			}
		}
	}()
	return sse.Read(resp.Body, func(name, _ string, data []byte) error {
		select {
		case alive <- struct{}{}:
		default:
		}
		if name != "message" && name != "deleted" {
			return nil
		}
		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if name == "deleted" {
			if m.Channel != channel || m.ID == "" {
				return nil
			}
			return n.applyDelete(m.Tombstone())
		}
		if m.Channel != channel || m.Seq <= 0 {
			return nil // a host may only publish its own board
		}
		if err := validate(&m); err != nil {
			return nil
		}
		return n.save(&m)
	})
}
