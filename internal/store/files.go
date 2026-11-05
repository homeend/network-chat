package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"netchat/internal/protocol"
)

// Cursors returns channel -> last acked seq for an identity.
func (s *Store) Cursors(identityID string) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT channel, last_ack_seq FROM cursors WHERE identity_id=?`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var ch string
		var seq int64
		if err := rows.Scan(&ch, &seq); err != nil {
			return nil, err
		}
		out[ch] = seq
	}
	return out, rows.Err()
}

// Ack moves a cursor forward (never backwards).
func (s *Store) Ack(identityID, channel string, seq int64) error {
	_, err := s.db.Exec(`INSERT INTO cursors(identity_id, channel, last_ack_seq) VALUES(?,?,?)
		ON CONFLICT(identity_id, channel) DO UPDATE SET last_ack_seq=MAX(last_ack_seq, excluded.last_ack_seq)`,
		identityID, channel, seq)
	return err
}

// MessageByID returns one stored message.
func (s *Store) MessageByID(id string) (protocol.Message, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM messages WHERE id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Message{}, ErrNotFound
	}
	if err != nil {
		return protocol.Message{}, err
	}
	var m protocol.Message
	return m, json.Unmarshal([]byte(body), &m)
}

// Replace overwrites a stored message (same id; used for tombstones).
// It reports false if the message is not stored.
func (s *Store) Replace(m protocol.Message) (bool, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	res, err := s.db.Exec(`UPDATE messages SET body=? WHERE id=? AND channel=?`, string(body), m.ID, m.Channel)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteFile forgets a file record.
func (s *Store) DeleteFile(id string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE id=?`, id)
	return err
}

func (s *Store) PutFile(f protocol.LocalFile) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO files(id, name, size, mime, sha256, machine, owned, path, created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		f.ID, f.Name, f.Size, f.Mime, f.SHA256, f.Machine, f.Owned, f.Path, f.CreatedAt.UnixMilli())
	return err
}

func (s *Store) File(id string) (protocol.LocalFile, error) {
	rows, err := s.db.Query(`SELECT id, name, size, mime, sha256, machine, owned, path, created_at FROM files WHERE id=?`, id)
	if err != nil {
		return protocol.LocalFile{}, err
	}
	fs, err := scanFiles(rows)
	if err != nil {
		return protocol.LocalFile{}, err
	}
	if len(fs) == 0 {
		return protocol.LocalFile{}, ErrNotFound
	}
	return fs[0], nil
}

func (s *Store) Files() ([]protocol.LocalFile, error) {
	rows, err := s.db.Query(`SELECT id, name, size, mime, sha256, machine, owned, path, created_at FROM files ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	return scanFiles(rows)
}

func scanFiles(rows *sql.Rows) ([]protocol.LocalFile, error) {
	defer rows.Close()
	out := []protocol.LocalFile{}
	for rows.Next() {
		var f protocol.LocalFile
		var ms int64
		if err := rows.Scan(&f.ID, &f.Name, &f.Size, &f.Mime, &f.SHA256, &f.Machine, &f.Owned, &f.Path, &ms); err != nil {
			return nil, err
		}
		f.CreatedAt = time.UnixMilli(ms)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FindFileRef finds a file offer by id in any stored message.
func (s *Store) FindFileRef(id string) (protocol.FileRef, error) {
	if id == "" || strings.ContainsAny(id, `%_"\`) {
		return protocol.FileRef{}, ErrNotFound
	}
	var body string
	err := s.db.QueryRow(`SELECT body FROM messages WHERE body LIKE ? ORDER BY ts DESC LIMIT 1`, `%"id":"`+id+`"%`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.FileRef{}, ErrNotFound
	}
	if err != nil {
		return protocol.FileRef{}, err
	}
	var m protocol.Message
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return protocol.FileRef{}, err
	}
	for _, f := range m.Files {
		if f.ID == id {
			return f, nil
		}
	}
	return protocol.FileRef{}, ErrNotFound
}
