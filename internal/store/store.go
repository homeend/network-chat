// Package store persists identities and messages in SQLite (pure Go driver).
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"netchat/internal/protocol"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS identities (
	id         TEXT PRIMARY KEY,
	nick       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
	id        TEXT PRIMARY KEY,
	channel   TEXT NOT NULL,
	seq       INTEGER NOT NULL,
	ts        INTEGER NOT NULL,
	from_id   TEXT NOT NULL,
	to_id     TEXT,
	body      TEXT NOT NULL,           -- full protocol.Message JSON
	UNIQUE(channel, seq)
);
CREATE INDEX IF NOT EXISTS messages_to ON messages(to_id, channel, seq);
CREATE TABLE IF NOT EXISTS cursors (     -- agent inbox read positions
	identity_id  TEXT NOT NULL,
	channel      TEXT NOT NULL,
	last_ack_seq INTEGER NOT NULL,
	PRIMARY KEY (identity_id, channel)
);
CREATE TABLE IF NOT EXISTS files (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	size       INTEGER NOT NULL,
	mime       TEXT NOT NULL,
	sha256     TEXT NOT NULL,
	machine    TEXT NOT NULL,
	owned      INTEGER NOT NULL,      -- 1: we offer it; 0: we downloaded it
	path       TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; seq assignment relies on it
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- identities ----

func (s *Store) Identities() ([]protocol.Identity, error) {
	rows, err := s.db.Query(`SELECT id, nick, kind, created_at FROM identities ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Identity
	for rows.Next() {
		var i protocol.Identity
		var ms int64
		if err := rows.Scan(&i.ID, &i.Nick, &i.Kind, &ms); err != nil {
			return nil, err
		}
		i.CreatedAt = time.UnixMilli(ms)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) CreateIdentity(i protocol.Identity) error {
	_, err := s.db.Exec(`INSERT INTO identities(id, nick, kind, created_at) VALUES(?,?,?,?)`,
		i.ID, i.Nick, i.Kind, i.CreatedAt.UnixMilli())
	return err
}

func (s *Store) DeleteIdentity(id string) error {
	res, err := s.db.Exec(`DELETE FROM identities WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- messages ----

// Insert stores m. If m.Seq is 0 the next seq of the channel is assigned
// (local authority: own board, DMs). Duplicate ids are ignored and reported
// with inserted=false. m is updated with the assigned seq.
func (s *Store) Insert(m *protocol.Message) (inserted bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE id=?`, m.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	if m.Seq == 0 {
		if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE channel=?`, m.Channel).Scan(&m.Seq); err != nil {
			return false, err
		}
	}
	body, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	var to any
	if m.To != nil {
		to = m.To.ID
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id, channel, seq, ts, from_id, to_id, body) VALUES(?,?,?,?,?,?,?)`,
		m.ID, m.Channel, m.Seq, m.TS.UnixMilli(), m.From.ID, to, string(body))
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil // (channel, seq) taken: stale cache entry from a reset host
	}
	return true, tx.Commit()
}

// Query lists messages of a channel in ascending seq order.
// afterSeq > 0: oldest first after it. Otherwise the latest `limit` (optionally before beforeSeq).
func (s *Store) Query(channel string, afterSeq, beforeSeq int64, limit int) ([]protocol.Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows *sql.Rows
	var err error
	switch {
	case afterSeq > 0:
		rows, err = s.db.Query(`SELECT body FROM messages WHERE channel=? AND seq>? ORDER BY seq ASC LIMIT ?`, channel, afterSeq, limit)
	case beforeSeq > 0:
		rows, err = s.db.Query(`SELECT body FROM (SELECT body, seq FROM messages WHERE channel=? AND seq<? ORDER BY seq DESC LIMIT ?) ORDER BY seq ASC`, channel, beforeSeq, limit)
	default:
		rows, err = s.db.Query(`SELECT body FROM (SELECT body, seq FROM messages WHERE channel=? ORDER BY seq DESC LIMIT ?) ORDER BY seq ASC`, channel, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanBodies(rows)
}

// Tail returns messages with seq > afterSeq but at most the newest `limit`
// (used to replay a board to a reconnecting subscriber).
func (s *Store) Tail(channel string, afterSeq int64, limit int) ([]protocol.Message, error) {
	rows, err := s.db.Query(`SELECT body FROM (SELECT body, seq FROM messages WHERE channel=? AND seq>? ORDER BY seq DESC LIMIT ?) ORDER BY seq ASC`, channel, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return scanBodies(rows)
}

func (s *Store) MaxSeq(channel string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE channel=?`, channel).Scan(&n)
	return n, err
}

// ChannelStat is the last activity of a channel.
type ChannelStat struct {
	Channel string
	LastSeq int64
	LastTS  time.Time
}

// Channels returns activity stats for all channels with the given prefix.
func (s *Store) Channels(prefix string) ([]ChannelStat, error) {
	rows, err := s.db.Query(`SELECT channel, MAX(seq), MAX(ts) FROM messages WHERE channel LIKE ? GROUP BY channel`,
		strings.ReplaceAll(prefix, "%", "")+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelStat
	for rows.Next() {
		var c ChannelStat
		var ms int64
		if err := rows.Scan(&c.Channel, &c.LastSeq, &ms); err != nil {
			return nil, err
		}
		c.LastTS = time.UnixMilli(ms)
		out = append(out, c)
	}
	return out, rows.Err()
}

// LastMessage returns the newest message of a channel.
func (s *Store) LastMessage(channel string) (protocol.Message, error) {
	ms, err := s.Query(channel, 0, 0, 1)
	if err != nil {
		return protocol.Message{}, err
	}
	if len(ms) == 0 {
		return protocol.Message{}, ErrNotFound
	}
	return ms[0], nil
}

func scanBodies(rows *sql.Rows) ([]protocol.Message, error) {
	defer rows.Close()
	out := []protocol.Message{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var m protocol.Message
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
