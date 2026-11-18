package store

import (
	"path/filepath"
	"testing"
	"time"

	"netchat/internal/ids"
	"netchat/internal/protocol"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func msg(ch string, seq int64) *protocol.Message {
	return &protocol.Message{ID: ids.New(), Channel: ch, Seq: seq, From: protocol.IdentityRef{ID: "a", Nick: "a"}, Text: "x", TS: time.Now()}
}

func TestInsertAssignsSeqAndDedupes(t *testing.T) {
	s := open(t)
	m1, m2 := msg("board:x", 0), msg("board:x", 0)
	for i, m := range []*protocol.Message{m1, m2} {
		ok, err := s.Insert(m)
		if err != nil || !ok || m.Seq != int64(i+1) {
			t.Fatalf("insert %d: ok=%v err=%v seq=%d", i, ok, err, m.Seq)
		}
	}
	dup := *m1
	dup.Seq = 0
	if ok, err := s.Insert(&dup); err != nil || ok {
		t.Fatalf("duplicate id inserted: ok=%v err=%v", ok, err)
	}
	// A cached remote message colliding on (channel, seq) is ignored, not an error.
	if ok, err := s.Insert(msg("board:x", 2)); err != nil || ok {
		t.Fatalf("seq collision: ok=%v err=%v", ok, err)
	}
	if n, _ := s.MaxSeq("board:x"); n != 2 {
		t.Fatalf("MaxSeq = %d", n)
	}
}

func TestQueryAndTail(t *testing.T) {
	s := open(t)
	for range 10 {
		if _, err := s.Insert(msg("c", 0)); err != nil {
			t.Fatal(err)
		}
	}
	seqs := func(ms []protocol.Message) (out []int64) {
		for _, m := range ms {
			out = append(out, m.Seq)
		}
		return
	}
	check := func(name string, ms []protocol.Message, err error, want ...int64) {
		t.Helper()
		got := seqs(ms)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s: got %v err %v, want %v", name, got, err, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}
	ms, err := s.Query("c", 0, 0, 3)
	check("latest", ms, err, 8, 9, 10)
	ms, err = s.Query("c", 7, 0, 2)
	check("after", ms, err, 8, 9)
	ms, err = s.Query("c", 0, 4, 2)
	check("before", ms, err, 2, 3)
	ms, err = s.Tail("c", 2, 3)
	check("tail capped to newest", ms, err, 8, 9, 10)
}

func TestCursorsNeverGoBack(t *testing.T) {
	s := open(t)
	_ = s.Ack("me", "board:a", 5)
	_ = s.Ack("me", "board:a", 3)
	c, err := s.Cursors("me")
	if err != nil || c["board:a"] != 5 {
		t.Fatalf("cursor = %v err %v", c, err)
	}
}
