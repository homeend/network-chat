package node

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"netchat/internal/protocol"
)

const MaxInboxWait = 60 * time.Second

// InboxFilter selects which sources feed an inbox.
type InboxFilter struct {
	DM, Board, Mention bool
}

func (f InboxFilter) all() bool { return !f.DM && !f.Board && !f.Mention }

// Inbox returns unacknowledged messages for a local identity, oldest first:
//   - dm:      direct messages to it
//   - board:   posts on this machine's channel
//   - mention: posts on any machine channel that @mention its nick
//
// Messages older than the identity, and its own messages, are skipped.
// Reading does not acknowledge (at-least-once); call Ack.
func (n *Node) Inbox(as protocol.Identity, f InboxFilter, limit int) ([]protocol.InboxItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if f.all() {
		f = InboxFilter{DM: true, Board: true, Mention: true}
	}
	cursors, err := n.Store.Cursors(as.ID)
	if err != nil {
		return nil, err
	}
	own := protocol.BoardChannel(n.Cfg.InstanceID)
	var channels []string
	if f.Board || f.Mention {
		bs, err := n.Store.Channels("board:")
		if err != nil {
			return nil, err
		}
		for _, b := range bs {
			channels = append(channels, b.Channel)
		}
	}
	if f.DM {
		ds, err := n.Store.Channels("dm:")
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if strings.Contains(d.Channel, as.ID) {
				channels = append(channels, d.Channel)
			}
		}
	}

	var out []protocol.InboxItem
	for _, ch := range channels {
		after := cursors[ch]
		for {
			ms, err := n.Store.Query(ch, after, 0, 200)
			if err != nil {
				return nil, err
			}
			for _, m := range ms {
				after = m.Seq
				if m.Deleted || m.From.ID == as.ID || m.TS.Before(as.CreatedAt) {
					continue
				}
				reason := ""
				switch {
				case strings.HasPrefix(ch, "dm:"):
					reason = "dm"
				case mentions(m.Text, as.Nick) && f.Mention:
					reason = "mention"
				case ch == own && f.Board:
					reason = "board"
				}
				if reason != "" {
					out = append(out, protocol.InboxItem{Reason: reason, Message: m})
				}
			}
			if len(ms) < 200 || len(out) >= limit*4 {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Message.TS.Before(out[j].Message.TS) })
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []protocol.InboxItem{}
	}
	return out, nil
}

// WaitInbox is Inbox with long-polling: if nothing is pending it waits up to
// `wait` (capped at MaxInboxWait) for a new message, then returns.
func (n *Node) WaitInbox(ctx context.Context, as protocol.Identity, f InboxFilter, limit int, wait time.Duration) ([]protocol.InboxItem, error) {
	wait = min(max(wait, 0), MaxInboxWait)
	events, cancel := n.Bus.Subscribe() // before the first read: no lost wake-ups
	defer cancel()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		items, err := n.Inbox(as, f, limit)
		if err != nil || len(items) > 0 || wait == 0 {
			return items, err
		}
	wake:
		for {
			select {
			case <-ctx.Done():
				return items, nil
			case <-deadline.C:
				return items, nil
			case e, ok := <-events:
				if !ok {
					return items, nil
				}
				if e.Type == "message" && e.Message != nil && e.Message.From.ID != as.ID {
					break wake
				}
			}
		}
	}
}

// Ack acknowledges messages by id for a local identity.
func (n *Node) Ack(as protocol.Identity, ids []string) (int, error) {
	acked := 0
	for _, id := range ids {
		m, err := n.Store.MessageByID(id)
		if err != nil {
			return acked, fmt.Errorf("%w: message %s", ErrNotFound, id)
		}
		if err := n.Store.Ack(as.ID, m.Channel, m.Seq); err != nil {
			return acked, err
		}
		acked++
	}
	return acked, nil
}

// mentions reports whether text contains @nick as a whole word (case-insensitive).
func mentions(text, nick string) bool {
	lt, ln := strings.ToLower(text), "@"+strings.ToLower(nick)
	for i := 0; ; {
		j := strings.Index(lt[i:], ln)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(ln)
		before := start == 0 || !isNickRune(rune(lt[start-1]))
		after := end == len(lt) || !isNickRune(rune(lt[end]))
		if before && after {
			return true
		}
		i = end
	}
}

func isNickRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '@'
}
