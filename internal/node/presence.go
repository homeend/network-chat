package node

import (
	"maps"
	"sync"
	"time"

	"netchat/internal/protocol"
)

// Presence of local identities, derived from what clients actually do:
// an agent is "active" while it long-polls its inbox (netchat wait, inbox
// --wait, agent --exec) or within ActiveWindow of its last call; "idle" up to
// IdleWindow; otherwise "offline". Human identities are active while the web
// UI is open. Changes are announced to peers right away.
const (
	ActiveWindow  = 90 * time.Second
	IdleWindow    = 15 * time.Minute
	presenceEvery = 10 * time.Second
)

type presence struct {
	mu        sync.Mutex
	last      map[string]time.Time // identity id -> last API activity
	waiting   map[string]int       // identity id -> open long-polls
	uiClients int                  // open web UI event streams
	announced map[string]string    // last states sent to peers
}

func newPresence() *presence {
	return &presence{last: map[string]time.Time{}, waiting: map[string]int{}, announced: map[string]string{}}
}

// Touch records activity of a local identity (send, ack, inbox...).
func (n *Node) Touch(id string) {
	n.pres.mu.Lock()
	n.pres.last[id] = time.Now()
	n.pres.mu.Unlock()
	n.checkPresence()
}

// BeginWait marks an open long-poll for an identity; call the returned func when it ends.
func (n *Node) BeginWait(id string) func() {
	n.pres.mu.Lock()
	n.pres.waiting[id]++
	n.pres.last[id] = time.Now()
	n.pres.mu.Unlock()
	n.checkPresence()
	return func() {
		n.pres.mu.Lock()
		n.pres.waiting[id]--
		if n.pres.waiting[id] <= 0 {
			delete(n.pres.waiting, id)
		}
		n.pres.last[id] = time.Now()
		n.pres.mu.Unlock()
		// no immediate check: a client usually re-polls at once; the ticker
		// demotes it after ActiveWindow if it does not.
	}
}

// UIConnected marks an open web UI; call the returned func when it closes.
func (n *Node) UIConnected() func() {
	n.pres.mu.Lock()
	n.pres.uiClients++
	n.pres.mu.Unlock()
	n.checkPresence()
	return func() {
		ids := n.LocalIdentities() // before taking pres.mu: no nested locks
		now := time.Now()
		n.pres.mu.Lock()
		n.pres.uiClients--
		for _, i := range ids {
			if i.Kind == protocol.KindHuman {
				n.pres.last[i.ID] = now // closing the UI counts as recent activity
			}
		}
		n.pres.mu.Unlock()
	}
}

// presenceOf computes the state of a local identity. Caller holds n.pres.mu.
func (n *Node) presenceOf(i protocol.Identity, now time.Time) (string, *time.Time) {
	p := n.pres
	var lastPtr *time.Time
	last, seen := p.last[i.ID]
	if seen {
		l := last
		lastPtr = &l
	}
	switch {
	case p.waiting[i.ID] > 0, i.Kind == protocol.KindHuman && p.uiClients > 0:
		return protocol.StateActive, &now
	case seen && now.Sub(last) <= ActiveWindow:
		return protocol.StateActive, lastPtr
	case seen && now.Sub(last) <= IdleWindow:
		return protocol.StateIdle, lastPtr
	}
	return protocol.StateOffline, lastPtr
}

// localRefs returns local identities with presence (for Info and the directory).
func (n *Node) localRefs() []protocol.IdentityRef {
	ids := n.LocalIdentities()
	now := time.Now()
	n.pres.mu.Lock()
	defer n.pres.mu.Unlock()
	refs := make([]protocol.IdentityRef, len(ids))
	for k, i := range ids {
		refs[k] = i.Ref()
		refs[k].State, refs[k].LastActive = n.presenceOf(i, now)
	}
	return refs
}

// checkPresence announces presence changes to the UI and to peers.
func (n *Node) checkPresence() {
	states := map[string]string{}
	for _, r := range n.localRefs() {
		states[r.ID] = r.State
	}
	n.pres.mu.Lock()
	changed := !maps.Equal(states, n.pres.announced)
	if changed {
		n.pres.announced = states
	}
	n.pres.mu.Unlock()
	if changed {
		n.identitiesChanged()
	}
}
