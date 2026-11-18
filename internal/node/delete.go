package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"netchat/internal/protocol"
	"netchat/internal/store"
)

var ErrForbidden = errors.New("forbidden")

// CanDelete reports whether this machine may remove m: messages sent from
// this machine (any of its identities, anywhere), and anything posted on this
// machine's own channel.
func (n *Node) CanDelete(m protocol.Message) bool {
	return m.FromMachine == n.Cfg.InstanceID || m.Channel == protocol.BoardChannel(n.Cfg.InstanceID)
}

// DeleteMessage removes a message for everyone. The board host, which holds
// the authoritative copy, must be reachable; nothing is
// changed locally if it refuses or is offline. Files offered with the message
// by this machine are withdrawn.
func (n *Node) DeleteMessage(ctx context.Context, id string) (protocol.Message, error) {
	m, err := n.Store.MessageByID(id)
	if errors.Is(err, store.ErrNotFound) {
		return m, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	if err != nil || m.Deleted {
		return m, err
	}
	if !n.CanDelete(m) {
		return m, fmt.Errorf("%w: you can remove only messages sent from this machine, or anything on its own channel", ErrForbidden)
	}
	req := protocol.DeleteRequest{ID: m.ID, Machine: n.Cfg.InstanceID}
	// Messages outside machine channels (0.1.0 direct messages) are only removed here.
	remote, _ := strings.CutPrefix(m.Channel, "board:")
	if strings.HasPrefix(m.Channel, "board:") && remote != n.Cfg.InstanceID {
		// Not found there = already gone (or never delivered): remove our copy anyway.
		if err := n.postToPeer(ctx, remote, "/p/board/delete", req, nil); err != nil && !errors.Is(err, ErrNotFound) {
			return m, err
		}
	}
	if m.FromMachine == n.Cfg.InstanceID {
		n.withdrawFiles(m.Files)
	}
	t := m.Tombstone()
	return t, n.applyDelete(t)
}

// AcceptDelete is the board host side of POST /p/board/delete.
func (n *Node) AcceptDelete(req protocol.DeleteRequest) error {
	m, err := n.Store.MessageByID(req.ID)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: message %s", ErrNotFound, req.ID)
	}
	if err != nil {
		return err
	}
	if m.Channel != protocol.BoardChannel(n.Cfg.InstanceID) {
		return fmt.Errorf("%w: message %s", ErrNotFound, req.ID)
	}
	if req.Machine == "" || m.FromMachine != req.Machine {
		return fmt.Errorf("%w: only the sender's machine may remove a message", ErrForbidden)
	}
	if m.Deleted {
		return nil
	}
	return n.applyDelete(m.Tombstone())
}

// applyDelete stores a tombstone over an existing message and announces it
// (UI, board subscribers). Unknown messages are ignored.
func (n *Node) applyDelete(t protocol.Message) error {
	replaced, err := n.Store.Replace(t)
	if err != nil || !replaced {
		return err
	}
	n.Bus.Publish(protocol.Event{Type: "deleted", Message: &t})
	return nil
}

// withdrawFiles stops offering our files; uploaded copies in the outbox are deleted.
func (n *Node) withdrawFiles(refs []protocol.FileRef) {
	outbox := filepath.Join(n.Cfg.DataDir, "outbox")
	for _, r := range refs {
		f, err := n.Store.File(r.ID)
		if err != nil || !f.Owned {
			continue
		}
		if err := n.Store.DeleteFile(f.ID); err != nil {
			n.Log.Warn("withdraw file", "file", f.Name, "err", err)
			continue
		}
		if filepath.Dir(f.Path) == outbox {
			_ = os.Remove(f.Path)
		}
	}
}
