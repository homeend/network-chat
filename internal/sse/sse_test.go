package sse

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	w := Start(rec)
	_ = w.Event("message", "7", map[string]string{"text": "line1\nline2"})
	_ = w.Ping()
	type ev struct{ name, id, data string }
	var got []ev
	err := Read(strings.NewReader(rec.Body.String()+": comment\n\n"), func(name, id string, data []byte) error {
		got = append(got, ev{name, id, string(data)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].name != "message" || got[0].id != "7" || got[0].data != `{"text":"line1\nline2"}` || got[1].name != "ping" {
		t.Fatalf("unexpected events: %+v", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
}
