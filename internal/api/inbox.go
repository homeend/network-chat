package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"netchat/internal/httpx"
	"netchat/internal/node"
	"netchat/internal/protocol"
)

// Agent inbox: GET /v1/inbox?as=<identity>&wait=<sec>&limit=<n>&only=dm,mention,board
// returns unacknowledged items (oldest first), long-polling up to `wait`
// seconds (max 60) when empty. POST /v1/ack {"as":..,"ids":[..]} acknowledges.
func registerInbox(mux *http.ServeMux, n *node.Node) {
	mux.HandleFunc("GET /v1/inbox", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		as, err := n.LocalIdentity(q.Get("as"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var f node.InboxFilter
		for _, s := range strings.Split(q.Get("only"), ",") {
			switch strings.TrimSpace(s) {
			case "dm":
				f.DM = true
			case "board":
				f.Board = true
			case "mention":
				f.Mention = true
			}
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		wait, _ := strconv.ParseFloat(q.Get("wait"), 64)
		if wait > 0 {
			defer n.BeginWait(as.ID)() // presence: someone listens as this identity
		} else {
			n.Touch(as.ID)
		}
		items, err := n.WaitInbox(r.Context(), as, f, limit, time.Duration(wait*float64(time.Second)))
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, items)
	})
	mux.HandleFunc("POST /v1/ack", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.AckRequest
		if err := httpx.Decode(r, 1<<20, &req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		as, err := n.LocalIdentity(req.As)
		if err != nil {
			writeErr(w, err)
			return
		}
		n.Touch(as.ID)
		acked, err := n.Ack(as, req.IDs)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]int{"acked": acked})
	})
}
