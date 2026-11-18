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
	m.Seq = 0
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

// Messages reads a channel from the local store.
func (n *Node) Messages(channel string, afterSeq, beforeSeq int64, limit int) ([]protocol.Message, error) {
	return n.Store.Query(channel, afterSeq, beforeSeq, limit)
}

// Channels lists the machine channels of all known machines.
func (n *Node) Channels() ([]protocol.ChannelInfo, error) {
	cs, err := n.Store.Channels("board:")
	if err != nil {
		return nil, err
	}
	stats := map[string]protocol.ChannelInfo{}
	for _, c := range cs {
		stats[c.Channel] = protocol.ChannelInfo{LastSeq: c.LastSeq, LastTS: c.LastTS}
	}
	var out []protocol.ChannelInfo
	for _, m := range n.Machines() {
		id := protocol.BoardChannel(m.InstanceID)
		ci := stats[id]
		ci.ID, ci.Kind, ci.Name, ci.MachineID, ci.Online = id, "board", "#"+m.Name, m.InstanceID, m.Online
		out = append(out, ci)
	}
	return out, nil
}
