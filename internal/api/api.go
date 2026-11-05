// Package api is the localhost-only control API used by the web UI, the CLI
// and agents.
package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"netchat/internal/httpx"
	"netchat/internal/node"
	"netchat/internal/protocol"
	"netchat/internal/sse"
	"netchat/internal/webui"
)

func Handler(n *node.Node) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.SelfInfo(false))
	})
	mux.HandleFunc("GET /v1/machines", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.Machines())
	})
	mux.HandleFunc("POST /v1/peers", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Addr string `json:"addr"`
		}
		if err := httpx.Decode(r, 4<<10, &body); err != nil || body.Addr == "" {
			httpx.Error(w, http.StatusBadRequest, "bad_request", `expected {"addr":"host[:port]"}`)
			return
		}
		info, err := n.AddPeer(r.Context(), body.Addr)
		if errors.Is(err, node.ErrSelf) {
			httpx.Error(w, http.StatusConflict, "self", err.Error())
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "unreachable", err.Error())
			return
		}
		httpx.JSON(w, http.StatusOK, info)
	})
	mux.HandleFunc("POST /v1/scan", func(w http.ResponseWriter, r *http.Request) {
		found := n.Scan(r.Context())
		httpx.JSON(w, http.StatusOK, map[string]int{"found": found})
	})

	// Identities
	mux.HandleFunc("GET /v1/identities", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.LocalIdentities())
	})
	mux.HandleFunc("POST /v1/identities", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Nick, Kind string }
		if err := httpx.Decode(r, 4<<10, &body); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		id, err := n.CreateIdentity(body.Nick, body.Kind)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, id)
	})
	mux.HandleFunc("DELETE /v1/identities/{sel}", func(w http.ResponseWriter, r *http.Request) {
		id, err := n.DeleteIdentity(r.PathValue("sel"))
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, id)
	})
	mux.HandleFunc("GET /v1/directory", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, n.Directory())
	})

	// Channels and messages
	mux.HandleFunc("GET /v1/channels", func(w http.ResponseWriter, r *http.Request) {
		cs, err := n.Channels()
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, cs)
	})
	mux.HandleFunc("GET /v1/channels/{channel}/messages", func(w http.ResponseWriter, r *http.Request) {
		ch, err := resolveChannel(n, r.PathValue("channel"), r.URL.Query().Get("as"))
		if err != nil {
			writeErr(w, err)
			return
		}
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after_seq"), 10, 64)
		before, _ := strconv.ParseInt(q.Get("before_seq"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		ms, err := n.Messages(ch, after, before, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, ms)
	})
	mux.HandleFunc("POST /v1/send", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.SendRequest
		if err := httpx.Decode(r, protocol.MaxTextBytes+8<<10, &req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		m, err := send(r, n, req, nil)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, m)
	})

	// Remove a message for everyone (own messages, or anything on our machine channel).
	mux.HandleFunc("DELETE /v1/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		m, err := n.DeleteMessage(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, m)
	})

	// Live events for the UI (agents should prefer the inbox, milestone 5).
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		events, cancel := n.Bus.Subscribe()
		defer cancel()
		if r.URL.Query().Get("ui") == "1" {
			defer n.UIConnected()() // the human is here while the UI is open
		}
		s := sse.Start(w)
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
				if !ok || s.Event(e.Type, "", e) != nil {
					return
				}
			}
		}
	})
	registerFiles(mux, n)
	registerInbox(mux, n)
	registerSkills(mux)
	mux.Handle("GET /", webui.Handler())
	return localOnly(mux)
}

func send(r *http.Request, n *node.Node, req protocol.SendRequest, files []protocol.FileRef) (protocol.Message, error) {
	as, err := n.LocalIdentity(req.As)
	if err != nil {
		return protocol.Message{}, err
	}
	n.Touch(as.ID)
	o := node.SendOpts{Intent: req.Intent, ReplyTo: req.ReplyTo, Hops: req.Hops, Files: files}
	to := strings.TrimSpace(req.To)
	switch {
	case strings.HasPrefix(to, "board:"):
		return n.PostBoard(r.Context(), as, strings.TrimPrefix(to, "board:"), req.Text, o)
	case strings.HasPrefix(to, "#"):
		m, err := n.ResolveMachine(to)
		if err != nil {
			return protocol.Message{}, err
		}
		return n.PostBoard(r.Context(), as, m.InstanceID, req.Text, o)
	case strings.HasPrefix(to, "dm:"):
		parts := strings.Split(to, ":")
		if len(parts) != 3 || (parts[1] != as.ID && parts[2] != as.ID) {
			return protocol.Message{}, fmt.Errorf("%w: %s is not a conversation of %s", node.ErrInvalid, to, as.Nick)
		}
		other := parts[1]
		if other == as.ID {
			other = parts[2]
		}
		to = other
	}
	peer, err := n.ResolveIdentity(to)
	if err != nil {
		return protocol.Message{}, err
	}
	return n.SendDM(r.Context(), as, peer, req.Text, o)
}

// resolveChannel accepts a channel id, "#machine" or "@nick" (DM with the
// given local identity `as`).
func resolveChannel(n *node.Node, sel, as string) (string, error) {
	switch {
	case strings.HasPrefix(sel, "board:"), strings.HasPrefix(sel, "dm:"):
		return sel, nil
	case strings.HasPrefix(sel, "#"):
		m, err := n.ResolveMachine(sel)
		if err != nil {
			return "", err
		}
		return protocol.BoardChannel(m.InstanceID), nil
	}
	me, err := n.LocalIdentity(as)
	if err != nil {
		return "", err
	}
	peer, err := n.ResolveIdentity(sel)
	if err != nil {
		return "", err
	}
	return protocol.DMChannel(me.ID, peer.ID), nil
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, node.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, node.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, node.ErrAmbiguous):
		httpx.Error(w, http.StatusConflict, "ambiguous", err.Error())
	case errors.Is(err, node.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, node.ErrOffline):
		httpx.Error(w, http.StatusServiceUnavailable, "offline", err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func isLocalHost(h string) bool {
	h = strings.Trim(h, "[]")
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

// localOnly rejects non-loopback clients and foreign Host headers (DNS
// rebinding protection: a web page on another origin must not drive the API).
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			httpx.Error(w, http.StatusForbidden, "forbidden", "control API is localhost-only")
			return
		}
		h := r.Host
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		if !isLocalHost(h) {
			httpx.Error(w, http.StatusForbidden, "forbidden", "bad Host header")
			return
		}
		// CSRF: browsers send Origin on cross-site requests; only our own UI may call.
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !isLocalHost(u.Hostname()) {
				httpx.Error(w, http.StatusForbidden, "forbidden", "cross-origin request")
				return
			}
		}
		if r.Method == http.MethodPost && r.ContentLength != 0 &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") { // neither is CORS-safelisted
			httpx.Error(w, http.StatusUnsupportedMediaType, "bad_request", "Content-Type must be application/json or application/octet-stream")
			return
		}
		next.ServeHTTP(w, r)
	})
}
