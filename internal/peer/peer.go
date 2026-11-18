// Package peer is the LAN-facing HTTP server (machine-to-machine traffic only).
// Nothing here may expose local identities' private data or act on their behalf.
package peer

import (
	"errors"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"netchat/internal/httpx"
	"netchat/internal/node"
	"netchat/internal/protocol"
	"netchat/internal/sse"
)

const maxBody = protocol.MaxTextBytes + 16<<10

func Handler(n *node.Node) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/info", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.SelfInfo(false))
	})
	mux.HandleFunc("POST /p/register", func(w http.ResponseWriter, r *http.Request) {
		var info protocol.Info
		if err := httpx.Decode(r, maxBody, &info); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if info.Proto != protocol.Proto || info.InstanceID == "" || info.Port <= 0 || info.Port > 65535 {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid info")
			return
		}
		if info.InstanceID == n.Cfg.InstanceID {
			httpx.Error(w, http.StatusConflict, "self", "that is me")
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "remote addr")
			return
		}
		n.Register(info, host) // trust the connection source, not self-reported addresses
		httpx.JSON(w, http.StatusOK, n.SelfInfo(false))
	})
	mux.HandleFunc("GET /p/peers", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.PeerEntries())
	})

	// Removal requests from the sender's machine.
	mux.HandleFunc("POST /p/board/delete", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.DeleteRequest
		if err := httpx.Decode(r, 4<<10, &req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := n.AcceptDelete(req); err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	// Our machine board: post, page history, live stream.
	board := protocol.BoardChannel(n.Cfg.InstanceID)
	mux.HandleFunc("POST /p/board", func(w http.ResponseWriter, r *http.Request) {
		var m protocol.Message
		if err := httpx.Decode(r, maxBody, &m); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		stored, err := n.AcceptBoardPost(m)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, stored)
	})
	mux.HandleFunc("GET /p/board", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
		before, _ := strconv.ParseInt(r.URL.Query().Get("before_seq"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > node.BoardBacklog {
			limit = node.BoardBacklog
		}
		ms, err := n.Messages(board, after, before, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, ms)
	})
	mux.HandleFunc("GET /p/board/events", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if v := r.Header.Get("Last-Event-ID"); v != "" {
			after, _ = strconv.ParseInt(v, 10, 64)
		}
		events, cancel := n.Bus.Subscribe() // subscribe before replay: no gap
		defer cancel()
		backlog, err := n.Store.Tail(board, after, node.BoardBacklog)
		if err != nil {
			writeErr(w, err)
			return
		}
		s := sse.Start(w)
		if after > 0 { // removals the subscriber may have missed while away
			recent, err := n.Store.Tail(board, 0, node.BoardBacklog)
			if err != nil {
				return
			}
			for i := range recent {
				if recent[i].Deleted && recent[i].Seq <= after && s.Event("deleted", "", recent[i]) != nil {
					return
				}
			}
		}
		last := after
		for i := range backlog {
			if err := s.Event("message", strconv.FormatInt(backlog[i].Seq, 10), backlog[i]); err != nil {
				return
			}
			last = backlog[i].Seq
		}
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				if s.Ping() != nil {
					return
				}
			case e, ok := <-events:
				if !ok {
					return
				}
				if e.Message == nil || e.Message.Channel != board {
					continue
				}
				if e.Type == "deleted" {
					if s.Event("deleted", "", e.Message) != nil {
						return
					}
					continue
				}
				if e.Type != "message" || e.Message.Seq <= last {
					continue
				}
				last = e.Message.Seq
				if s.Event("message", strconv.FormatInt(last, 10), e.Message) != nil {
					return
				}
			}
		}
	})
	// Files we offer. The random id is the access token.
	mux.HandleFunc("GET /p/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, err := n.OwnedFile(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		fh, err := os.Open(f.Path)
		if err != nil {
			httpx.Error(w, http.StatusGone, "gone", "file no longer available")
			return
		}
		defer fh.Close()
		st, err := fh.Stat()
		if err != nil {
			httpx.Error(w, http.StatusGone, "gone", "file no longer available")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("ETag", `"`+f.SHA256+`"`) // enables If-Range for resume
		http.ServeContent(w, r, "", st.ModTime(), fh)
	})
	return mux
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, node.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, node.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, node.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
