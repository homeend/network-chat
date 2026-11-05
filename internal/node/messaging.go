package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"netchat/internal/ids"
	"netchat/internal/protocol"
)

const sendTimeout = 5 * time.Second

var ErrOffline = errors.New("offline")

// SendOpts are optional message fields.
type SendOpts struct {
	Intent  string
	ReplyTo string
	Hops    int
	Files   []protocol.FileRef
}

func (n *Node) newMessage(as protocol.Identity, text string, o SendOpts) (protocol.Message, error) {
	m := protocol.Message{
		ID: ids.New(), From: as.Ref(), FromMachine: n.Cfg.InstanceID, FromMachineName: n.Cfg.Name,
		Text: text, Intent: o.Intent, ReplyTo: o.ReplyTo, Hops: o.Hops, Files: o.Files, TS: time.Now().UTC(),
	}
	return m, validate(&m)
}

var (
	hexRe = regexp.MustCompile(`^[0-9a-f]+$`)
)

func validateFiles(m *protocol.Message) error {
	if len(m.Files) > 20 {
		return fmt.Errorf("%w: at most 20 files per message", ErrInvalid)
	}
	for _, f := range m.Files {
		switch {
		case len(f.ID) != 32 || !hexRe.MatchString(f.ID):
			return fmt.Errorf("%w: bad file id", ErrInvalid)
		case len(f.SHA256) != 64 || !hexRe.MatchString(f.SHA256):
			return fmt.Errorf("%w: bad file hash", ErrInvalid)
		case f.Name == "" || len(f.Name) > 255 || f.Size < 0:
			return fmt.Errorf("%w: bad file name or size", ErrInvalid)
		case f.Machine != m.FromMachine:
			return fmt.Errorf("%w: files must be offered by the sender's machine", ErrInvalid)
		}
	}
	return nil
}

func validate(m *protocol.Message) error {
	if m.Deleted { // tombstone replayed by a board host
		*m = m.Tombstone()
	}
	switch {
	case m.ID == "" || len(m.ID) > 64:
		return fmt.Errorf("%w: bad id", ErrInvalid)
	case m.From.ID == "" || m.From.Nick == "" || m.FromMachine == "":
		return fmt.Errorf("%w: missing sender", ErrInvalid)
	case len(m.Text) > protocol.MaxTextBytes:
		return fmt.Errorf("%w: text longer than %d bytes", ErrInvalid, protocol.MaxTextBytes)
	case strings.TrimSpace(m.Text) == "" && len(m.Files) == 0 && !m.Deleted:
		return fmt.Errorf("%w: empty message", ErrInvalid)
	case m.Hops < 0 || m.Hops > protocol.MaxHops:
		return fmt.Errorf("%w: hops limit exceeded (%d)", ErrInvalid, protocol.MaxHops)
	}
	switch m.Intent {
	case "", protocol.IntentAsk, protocol.IntentAnswer, protocol.IntentNotify:
	default:
		return fmt.Errorf("%w: intent must be ask, answer or notify", ErrInvalid)
	}
	if m.From.Kind != protocol.KindAgent {
		m.From.Kind = protocol.KindHuman
	}
	m.From = m.From.Plain() // presence never travels inside messages
	if m.To != nil {
		to := m.To.Plain()
		m.To = &to
	}
	return validateFiles(m)
}

// save stores m and publishes it if it was new.
func (n *Node) save(m *protocol.Message) error {
	inserted, err := n.Store.Insert(m)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if m.Channel == protocol.BoardChannel(n.Cfg.InstanceID) {
		n.boardSeq.Store(m.Seq)
	}
	n.Bus.Publish(protocol.Event{Type: "message", Message: m})
	return nil
}

// PostBoard posts to a machine channel (local or remote host).
func (n *Node) PostBoard(ctx context.Context, as protocol.Identity, machineID, text string, o SendOpts) (protocol.Message, error) {
	m, err := n.newMessage(as, text, o)
	if err != nil {
		return m, err
	}
	m.Channel = protocol.BoardChannel(machineID)
	if machineID == n.Cfg.InstanceID {
		return m, n.save(&m)
	}
	var stored protocol.Message
	if err := n.postToPeer(ctx, machineID, "/p/board", m, &stored); err != nil {
		return m, err
	}
	if stored.ID != m.ID || stored.Channel != m.Channel {
		return m, fmt.Errorf("host returned a different message")
	}
	return stored, n.save(&stored) // usually also arrives via the board subscription; deduped
}

// SendDM delivers a direct message live. Nothing is stored if delivery fails.
func (n *Node) SendDM(ctx context.Context, as protocol.Identity, to protocol.DirEntry, text string, o SendOpts) (protocol.Message, error) {
	m, err := n.newMessage(as, text, o)
	if err != nil {
		return m, err
	}
	ref := to.IdentityRef.Plain()
	m.To, m.ToMachine, m.Channel = &ref, to.MachineID, protocol.DMChannel(as.ID, to.ID)
	if to.MachineID != n.Cfg.InstanceID {
		if err := n.postToPeer(ctx, to.MachineID, "/p/dm", m, nil); err != nil {
			return m, err
		}
	}
	m.Seq = 0 // local seq
	return m, n.save(&m)
}

func (n *Node) postToPeer(ctx context.Context, machineID, path string, in, out any) error {
	p, ok := n.Peers.Get(machineID)
	if !ok || !p.Online(time.Now()) {
		return fmt.Errorf("%w: machine is not reachable", ErrOffline)
	}
	body, _ := json.Marshal(in)
	c, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, "http://"+p.Addr()+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s did not answer (%v)", ErrOffline, p.Info.Machine, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e protocol.ErrorBody
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e)
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %s", ErrNotFound, e.Error.Message)
		}
		return fmt.Errorf("%s rejected: HTTP %d %s", p.Info.Machine, resp.StatusCode, e.Error.Message)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
	}
	return nil
}

// AcceptBoardPost is the host side of POST /p/board.
func (n *Node) AcceptBoardPost(m protocol.Message) (protocol.Message, error) {
	if m.Deleted {
		return m, fmt.Errorf("%w: cannot post a removed message", ErrInvalid)
	}
	if err := validate(&m); err != nil {
		return m, err
	}
	m.Channel = protocol.BoardChannel(n.Cfg.InstanceID)
	m.Seq, m.To, m.ToMachine = 0, nil, ""
	m.TS = time.Now().UTC() // host clock orders the board
	if err := n.save(&m); err != nil {
		return m, err
	}
	if m.Seq == 0 { // duplicate post (retry): return the stored copy
		if ms, err := n.Store.Query(m.Channel, 0, 0, 200); err == nil {
			for _, x := range ms {
				if x.ID == m.ID {
					return x, nil
				}
			}
		}
	}
	return m, nil
}

// AcceptDM is the recipient side of POST /p/dm.
func (n *Node) AcceptDM(m protocol.Message) error {
	if m.Deleted {
		return fmt.Errorf("%w: cannot send a removed message", ErrInvalid)
	}
	if err := validate(&m); err != nil {
		return err
	}
	if m.To == nil {
		return fmt.Errorf("%w: missing recipient", ErrInvalid)
	}
	var to protocol.Identity
	found := false
	for _, i := range n.LocalIdentities() {
		if i.ID == m.To.ID {
			to, found = i, true
		}
	}
	if !found {
		return fmt.Errorf("%w: no identity %s on %s", ErrNotFound, m.To.ID, n.Cfg.Name)
	}
	ref := to.Ref()
	m.To, m.ToMachine = &ref, n.Cfg.InstanceID
	m.Channel = protocol.DMChannel(m.From.ID, to.ID)
	m.Seq = 0
	return n.save(&m)
}

// Messages reads a channel from the local store.
func (n *Node) Messages(channel string, afterSeq, beforeSeq int64, limit int) ([]protocol.Message, error) {
	return n.Store.Query(channel, afterSeq, beforeSeq, limit)
}

// Channels lists all machine boards (known machines) and DM conversations.
func (n *Node) Channels() ([]protocol.ChannelInfo, error) {
	stats := map[string]protocol.ChannelInfo{}
	for _, prefix := range []string{"board:", "dm:"} {
		cs, err := n.Store.Channels(prefix)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			stats[c.Channel] = protocol.ChannelInfo{LastSeq: c.LastSeq, LastTS: c.LastTS}
		}
	}
	var out []protocol.ChannelInfo
	for _, m := range n.Machines() {
		id := protocol.BoardChannel(m.InstanceID)
		ci := stats[id]
		ci.ID, ci.Kind, ci.Name, ci.MachineID, ci.Online = id, "board", "#"+m.Name, m.InstanceID, m.Online
		out = append(out, ci)
	}

	dir := map[string]protocol.DirEntry{}
	for _, e := range n.Directory() {
		dir[e.ID] = e
	}
	var dms []protocol.ChannelInfo
	for id, ci := range stats {
		if !strings.HasPrefix(id, "dm:") {
			continue
		}
		parts := strings.Split(id, ":")
		if len(parts) != 3 {
			continue
		}
		last, err := n.Store.LastMessage(id)
		if err != nil {
			continue
		}
		a, b := parts[1], parts[2]
		meID, peerID := a, b
		if e, ok := dir[b]; ok && e.Local && !(dir[a].Local) {
			meID, peerID = b, a
		}
		me := refFor(meID, dir, last)
		peer := protocol.DirEntry{IdentityRef: refFor(peerID, dir, last)}
		if e, ok := dir[peerID]; ok {
			peer = e
		} else if last.From.ID == peerID {
			peer.MachineID, peer.MachineName = last.FromMachine, last.FromMachineName
		} else {
			peer.MachineID = last.ToMachine
		}
		ci.ID, ci.Kind, ci.Name, ci.Online = id, "dm", "@"+peer.Nick, peer.Online
		ci.Peer, ci.Me = &peer, &me
		dms = append(dms, ci)
	}
	sort.Slice(dms, func(i, j int) bool { return dms[i].LastTS.After(dms[j].LastTS) })
	return append(out, dms...), nil
}

func refFor(id string, dir map[string]protocol.DirEntry, last protocol.Message) protocol.IdentityRef {
	if e, ok := dir[id]; ok {
		return e.IdentityRef
	}
	if last.From.ID == id {
		return last.From
	}
	if last.To != nil && last.To.ID == id {
		return *last.To
	}
	return protocol.IdentityRef{ID: id, Nick: id[:min(8, len(id))]}
}
