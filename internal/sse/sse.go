// Package sse writes and reads Server-Sent Events.
package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Writer struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// Start sets SSE headers and flushes them.
func Start(w http.ResponseWriter) *Writer {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &Writer{w: w, rc: http.NewResponseController(w)}
	fmt.Fprint(w, "retry: 3000\n\n")
	_ = s.rc.Flush()
	return s
}

// Event writes one event with JSON data; id may be empty.
func (s *Writer) Event(name, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if id != "" {
		fmt.Fprintf(s.w, "id: %s\n", id)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Ping sends a "ping" event (a real event, so readers can detect liveness).
func (s *Writer) Ping() error { return s.Event("ping", "", struct{}{}) }

// Read parses an SSE stream and calls fn for each event until r ends or fn errors.
func Read(r io.Reader, fn func(name, id string, data []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var name, id string
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if data.Len() > 0 {
				if name == "" {
					name = "message"
				}
				if err := fn(name, id, []byte(data.String())); err != nil {
					return err
				}
			}
			name, id = "", ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, val, _ := strings.Cut(line, ":")
		val = strings.TrimPrefix(val, " ")
		switch field {
		case "event":
			name = val
		case "id":
			id = val
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(val)
		}
	}
	return sc.Err()
}
