// Package protocol holds wire types shared by the LAN peer protocol,
// the local control API and the CLI.
package protocol

import "time"

const (
	Proto          = "netchat/1"
	Version        = "0.1.0"
	DefaultPort    = 47800 // UDP beacons + LAN HTTP (same number, one firewall rule)
	DefaultAPIPort = 47801 // localhost-only control API
	DefaultGroup   = "224.0.0.178"
)

// IdentityRef is the public view of an addressable identity (human or agent).
type IdentityRef struct {
	ID   string `json:"id"`
	Nick string `json:"nick"`
	Kind string `json:"kind"` // "human" | "agent"
	// Presence, only in Info / directory (never in messages):
	// active = someone listens as this identity now (or acted < 90 s ago),
	// idle = acted within 15 min, offline = not connected.
	State      string     `json:"state,omitempty"`
	LastActive *time.Time `json:"lastActive,omitempty"`
}

const (
	StateActive  = "active"
	StateIdle    = "idle"
	StateOffline = "offline"
)

// Plain returns the ref without presence (for embedding in messages).
func (r IdentityRef) Plain() IdentityRef { return IdentityRef{ID: r.ID, Nick: r.Nick, Kind: r.Kind} }

// Info describes a running instance. It is sent as the UDP beacon payload
// and returned by GET /p/info and POST /p/register.
type Info struct {
	Proto         string        `json:"proto"`
	Version       string        `json:"version"`
	InstanceID    string        `json:"instanceId"`
	Machine       string        `json:"machine"`
	Port          int           `json:"port"`
	Identities    []IdentityRef `json:"identities,omitempty"`
	IdentitiesRev int64         `json:"identitiesRev"`
	BoardSeq      int64         `json:"boardSeq"`
	Announce      bool          `json:"announce,omitempty"` // beacon only: "please register with me"
}

// PeerEntry is one row of GET /p/peers (gossip).
type PeerEntry struct {
	InstanceID string `json:"instanceId"`
	Machine    string `json:"machine"`
	Addr       string `json:"addr"` // ip:port as seen by the reporting peer
}

// Machine is the control-API view of a known instance.
type Machine struct {
	InstanceID string        `json:"instanceId"`
	Name       string        `json:"name"`
	Addr       string        `json:"addr,omitempty"`
	Self       bool          `json:"self"`
	Online     bool          `json:"online"`
	LastSeen   time.Time     `json:"lastSeen"`
	Via        string        `json:"via,omitempty"` // beacon | register | manual | gossip | scan
	Version    string        `json:"version,omitempty"`
	Identities []IdentityRef `json:"identities,omitempty"`
}

// Identity is a local identity as stored on its own machine.
type Identity struct {
	ID        string    `json:"id"`
	Nick      string    `json:"nick"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

func (i Identity) Ref() IdentityRef { return IdentityRef{ID: i.ID, Nick: i.Nick, Kind: i.Kind} }

// DirEntry is an addressable identity anywhere on the network.
type DirEntry struct {
	IdentityRef
	MachineID   string `json:"machineId"`
	MachineName string `json:"machineName"`
	Local       bool   `json:"local"`
	Online      bool   `json:"online"`
}

const (
	KindHuman = "human"
	KindAgent = "agent"

	IntentAsk    = "ask"
	IntentAnswer = "answer"
	IntentNotify = "notify"

	MaxTextBytes = 64 << 10
	MaxHops      = 12
)

// Message is a chat message on a machine board or in a DM.
//
// Channel ids: "board:<instanceId>" for a machine channel (seq assigned by
// the hosting machine) and "dm:<idA>:<idB>" (sorted identity ids; seq is
// assigned locally by each side).
type Message struct {
	ID              string       `json:"id"`
	Channel         string       `json:"channel"`
	Seq             int64        `json:"seq"`
	From            IdentityRef  `json:"from"`
	FromMachine     string       `json:"fromMachine"`
	FromMachineName string       `json:"fromMachineName"`
	To              *IdentityRef `json:"to,omitempty"` // DMs only
	ToMachine       string       `json:"toMachine,omitempty"`
	Text            string       `json:"text"`
	Intent          string       `json:"intent,omitempty"`
	Hops            int          `json:"hops,omitempty"`
	ReplyTo         string       `json:"replyTo,omitempty"`
	Files           []FileRef    `json:"files,omitempty"`
	TS              time.Time    `json:"ts"`
	// Deleted marks a removed message (tombstone): text, files and intent are
	// cleared, id/channel/seq/sender/ts stay so history keeps its order.
	Deleted bool `json:"deleted,omitempty"`
}

// Tombstone returns the removed form of m.
func (m Message) Tombstone() Message {
	m.Text, m.Intent, m.Files, m.Deleted = "", "", nil, true
	return m
}

// DeleteRequest asks the machine holding a message (board host or DM
// recipient) to remove it: POST /p/board/delete, POST /p/dm/delete.
// Machine is the requesting instance; only the sender's machine (or the
// board host itself) may remove a message.
type DeleteRequest struct {
	ID      string `json:"id"`
	Machine string `json:"machine"`
}

// FileRef is a file offered in a message. The bytes stay on the offering
// machine until someone pulls them (GET /p/files/{id}); the 128-bit random
// id doubles as the access token.
type FileRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"` // display only; never used as a path as-is
	Size    int64  `json:"size"`
	Mime    string `json:"mime,omitempty"`
	SHA256  string `json:"sha256"`
	Machine string `json:"machine"` // instance id of the owner
}

// LocalFile is a file this machine offers (Owned) or has downloaded.
type LocalFile struct {
	FileRef
	Owned     bool      `json:"owned"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
}

// FileStatus reports download progress on the local event stream.
type FileStatus struct {
	ID    string `json:"id"`
	State string `json:"state"` // downloading | done | error
	Bytes int64  `json:"bytes"`
	Total int64  `json:"total"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

func BoardChannel(instanceID string) string { return "board:" + instanceID }

func DMChannel(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return "dm:" + a + ":" + b
}

// ChannelInfo is a row of GET /v1/channels.
type ChannelInfo struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"` // board | dm
	Name      string       `json:"name"`
	MachineID string       `json:"machineId,omitempty"` // board host
	Online    bool         `json:"online"`
	Peer      *DirEntry    `json:"peer,omitempty"` // dm: the other side
	Me        *IdentityRef `json:"me,omitempty"`   // dm: our side
	LastSeq   int64        `json:"lastSeq"`
	LastTS    time.Time    `json:"lastTs"`
}

// Event is pushed on the local SSE stream (/v1/events).
type Event struct {
	Type    string      `json:"type"` // message | deleted | identities | file
	Message *Message    `json:"message,omitempty"`
	File    *FileStatus `json:"file,omitempty"`
}

// SendRequest is the body of POST /v1/send.
//
// To accepts: "#machine" (machine channel; "#" = this machine), "@nick",
// "nick@machine", an identity id, or a channel id ("board:<id>", "dm:<a>:<b>").
type SendRequest struct {
	As      string `json:"as,omitempty"` // local identity nick/id; default: first human
	To      string `json:"to"`
	Text    string `json:"text"`
	Intent  string `json:"intent,omitempty"`
	ReplyTo string `json:"replyTo,omitempty"`
	Hops    int    `json:"hops,omitempty"`
}

// InboxItem is one unacknowledged message for an identity.
type InboxItem struct {
	Reason  string  `json:"reason"` // dm | board | mention
	Message Message `json:"message"`
}

// AckRequest acknowledges inbox items by message id (POST /v1/ack).
type AckRequest struct {
	As  string   `json:"as"`
	IDs []string `json:"ids"`
}

// ErrorBody is the single error envelope used by both HTTP servers.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
