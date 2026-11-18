package node

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"regexp"
	"strings"
	"time"

	"netchat/internal/ids"
	"netchat/internal/protocol"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
	ErrInvalid   = errors.New("invalid")
	nickRe       = regexp.MustCompile(`^[\p{L}\p{N}_.\-]{1,32}$`)
)

func (n *Node) loadIdentities() error {
	list, err := n.Store.Identities()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		id := protocol.Identity{ID: ids.New(), Nick: defaultNick(), Kind: protocol.KindHuman, CreatedAt: time.Now()}
		if err := n.Store.CreateIdentity(id); err != nil {
			return err
		}
		list = []protocol.Identity{id}
		n.Log.Info("created default identity", "nick", id.Nick)
	}
	n.idMu.Lock()
	n.idents, n.idRev = list, time.Now().UnixMilli()
	n.idMu.Unlock()
	return nil
}

func defaultNick() string {
	name := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
	}
	name = strings.Map(func(r rune) rune {
		if nickRe.MatchString(string(r)) {
			return r
		}
		return '_'
	}, name)
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// LocalIdentities returns a copy of this machine's identities.
func (n *Node) LocalIdentities() []protocol.Identity {
	n.idMu.RLock()
	defer n.idMu.RUnlock()
	return append([]protocol.Identity(nil), n.idents...)
}

func (n *Node) CreateIdentity(nick, kind string) (protocol.Identity, error) {
	nick = strings.TrimPrefix(strings.TrimSpace(nick), "@")
	if !nickRe.MatchString(nick) {
		return protocol.Identity{}, fmt.Errorf("%w: nick must be 1-32 letters, digits, _ . -", ErrInvalid)
	}
	if kind == "" {
		kind = protocol.KindHuman
	}
	if kind != protocol.KindHuman && kind != protocol.KindAgent {
		return protocol.Identity{}, fmt.Errorf("%w: kind must be human or agent", ErrInvalid)
	}
	for _, i := range n.LocalIdentities() {
		if strings.EqualFold(i.Nick, nick) {
			return protocol.Identity{}, fmt.Errorf("%w: nick %q already exists on this machine", ErrInvalid, nick)
		}
	}
	id := protocol.Identity{ID: ids.New(), Nick: nick, Kind: kind, CreatedAt: time.Now()}
	if err := n.Store.CreateIdentity(id); err != nil {
		return id, err
	}
	n.idMu.Lock()
	n.idents = append(n.idents, id)
	n.idMu.Unlock()
	n.identitiesChanged()
	return id, nil
}

func (n *Node) DeleteIdentity(sel string) (protocol.Identity, error) {
	id, err := n.LocalIdentity(sel)
	if err != nil {
		return id, err
	}
	if len(n.LocalIdentities()) == 1 {
		return id, fmt.Errorf("%w: cannot delete the last identity", ErrInvalid)
	}
	if err := n.Store.DeleteIdentity(id.ID); err != nil {
		return id, err
	}
	n.idMu.Lock()
	for i := range n.idents {
		if n.idents[i].ID == id.ID {
			n.idents = append(n.idents[:i], n.idents[i+1:]...)
			break
		}
	}
	n.idMu.Unlock()
	n.identitiesChanged()
	return id, nil
}

// identitiesChanged bumps the revision and tells everyone quickly: a LAN
// beacon plus a direct contact to known peers (covers other subnets).
func (n *Node) identitiesChanged() {
	n.idMu.Lock()
	n.idRev = time.Now().UnixMilli()
	n.idMu.Unlock()
	n.Bus.Publish(protocol.Event{Type: "identities"})
	if n.disc != nil {
		n.disc.Announce(false)
	}
	now := time.Now()
	for _, p := range n.Peers.Snapshot() {
		if !p.Online(now) {
			continue
		}
		go func(addr string) {
			ctx, cancel := context.WithTimeout(context.Background(), contactTimeout)
			defer cancel()
			_, _ = n.Contact(ctx, addr, "liveness")
		}(p.Addr())
	}
}

// LocalIdentity selects a local identity by nick, id or id prefix. An empty
// selector means the default: the first human identity (else the first one).
func (n *Node) LocalIdentity(sel string) (protocol.Identity, error) {
	list := n.LocalIdentities()
	sel = strings.TrimPrefix(strings.TrimSpace(sel), "@")
	if sel == "" {
		for _, i := range list {
			if i.Kind == protocol.KindHuman {
				return i, nil
			}
		}
		if len(list) > 0 {
			return list[0], nil
		}
		return protocol.Identity{}, fmt.Errorf("%w: no local identity", ErrNotFound)
	}
	for _, i := range list {
		if i.ID == sel || strings.EqualFold(i.Nick, sel) {
			return i, nil
		}
	}
	var hits []protocol.Identity
	if len(sel) >= 4 {
		for _, i := range list {
			if idMatches(i.ID, sel) {
				hits = append(hits, i)
			}
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	return protocol.Identity{}, fmt.Errorf("%w: no local identity %q", ErrNotFound, sel)
}

// Directory lists every addressable identity: local ones and those
// advertised by known peers.
func (n *Node) Directory() []protocol.DirEntry {
	var out []protocol.DirEntry
	for _, r := range n.localRefs() {
		out = append(out, protocol.DirEntry{IdentityRef: r, MachineID: n.Cfg.InstanceID, MachineName: n.Cfg.Name, Local: true, Online: true})
	}
	now := time.Now()
	for _, p := range n.Peers.Snapshot() {
		online := p.Online(now)
		for _, r := range p.Info.Identities {
			if !online {
				r.State = protocol.StateOffline // its machine is gone
			}
			out = append(out, protocol.DirEntry{IdentityRef: r, MachineID: p.Info.InstanceID, MachineName: p.Info.Machine, Online: online})
		}
	}
	return out
}

// ResolveMachine finds a machine by "#name", name, or instance id prefix.
// "" or "#" means this machine.
func (n *Node) ResolveMachine(target string) (protocol.Machine, error) {
	t := strings.TrimPrefix(strings.TrimSpace(target), "#")
	ms := n.Machines()
	if t == "" {
		return ms[0], nil
	}
	var hits []protocol.Machine
	for _, m := range ms {
		if strings.EqualFold(m.Name, t) || m.InstanceID == t {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 && len(t) >= 4 {
		for _, m := range ms {
			if strings.HasPrefix(m.InstanceID, strings.ToLower(t)) {
				hits = append(hits, m)
			}
		}
	}
	return pickOne(hits, target, func(m protocol.Machine) string {
		return fmt.Sprintf("%s (%s)", m.Name, m.InstanceID[:8])
	})
}

// ShortID is the distinguishing tail of a ULID (its head is a timestamp).
func ShortID(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

// idMatches accepts a prefix or suffix (>= 4 chars) of an identity id.
func idMatches(id, sel string) bool {
	sel = strings.ToUpper(sel)
	return strings.HasPrefix(id, sel) || strings.HasSuffix(id, sel)
}

func pickOne[T any](hits []T, target string, label func(T) string) (T, error) {
	var zero T
	switch len(hits) {
	case 0:
		return zero, fmt.Errorf("%w: %q", ErrNotFound, target)
	case 1:
		return hits[0], nil
	}
	var names []string
	for _, h := range hits {
		names = append(names, label(h))
	}
	return zero, fmt.Errorf("%w: %q matches %s", ErrAmbiguous, target, strings.Join(names, ", "))
}
