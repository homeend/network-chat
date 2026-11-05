package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"strings"

	"netchat/internal/httpx"
	"netchat/internal/node"
	"netchat/internal/osutil"
	"netchat/internal/protocol"
)

// OfferRequest is the body of POST /v1/offer (share a file by local path).
type OfferRequest struct {
	protocol.SendRequest
	Paths []string `json:"paths"`
}

// Image types shown inline by GET /v1/files/{id}/content; everything else is
// served as a download so nothing can run on the UI's origin.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func registerFiles(mux *http.ServeMux, n *node.Node) {
	// Share files by local path (CLI, agents).
	mux.HandleFunc("POST /v1/offer", func(w http.ResponseWriter, r *http.Request) {
		var req OfferRequest
		if err := httpx.Decode(r, 1<<20, &req); err != nil || len(req.Paths) == 0 {
			httpx.Error(w, http.StatusBadRequest, "bad_request", `expected {"to":..., "paths":[...], "text":...}`)
			return
		}
		var refs []protocol.FileRef
		for _, p := range req.Paths {
			ref, err := n.OfferPath(p)
			if err != nil {
				writeErr(w, err)
				return
			}
			refs = append(refs, ref)
		}
		m, err := send(r, n, req.SendRequest, refs)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, m)
	})

	// Share one file by upload (UI drag & drop): raw body, metadata in the query.
	mux.HandleFunc("POST /v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		name := q.Get("name")
		if name == "" || q.Get("to") == "" {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "name and to are required")
			return
		}
		// Resolve the target before storing bytes, to fail fast.
		if _, err := n.LocalIdentity(q.Get("as")); err != nil {
			writeErr(w, err)
			return
		}
		ref, err := n.OfferUpload(name, q.Get("mime"), r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		m, err := send(r, n, protocol.SendRequest{As: q.Get("as"), To: q.Get("to"), Text: q.Get("text")}, []protocol.FileRef{ref})
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, m)
	})

	mux.HandleFunc("GET /v1/files", func(w http.ResponseWriter, r *http.Request) {
		fs, err := n.Store.Files()
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, fs)
	})

	mux.HandleFunc("POST /v1/files/{id}/pull", func(w http.ResponseWriter, r *http.Request) {
		// Detached from the request: closing the UI tab must not abort the download.
		lf, err := n.Pull(context.WithoutCancel(r.Context()), r.PathValue("id"))
		if errors.Is(err, node.ErrBusy) {
			httpx.Error(w, http.StatusConflict, "busy", err.Error())
			return
		}
		if errors.Is(err, node.ErrGone) {
			httpx.Error(w, http.StatusGone, "gone", err.Error())
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, lf)
	})

	mux.HandleFunc("GET /v1/files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		lf, err := n.Store.File(r.PathValue("id"))
		if err != nil {
			httpx.Error(w, http.StatusNotFound, "not_found", "file is not on this machine (pull it first)")
			return
		}
		fh, err := os.Open(lf.Path)
		if err != nil {
			httpx.Error(w, http.StatusGone, "gone", "file was moved or deleted")
			return
		}
		defer fh.Close()
		st, _ := fh.Stat()
		h := w.Header()
		ct := strings.ToLower(lf.Mime)
		disp := "attachment"
		if inlineTypes[ct] {
			disp = "inline"
		} else {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": lf.Name}))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		http.ServeContent(w, r, "", st.ModTime(), fh)
	})

	mux.HandleFunc("POST /v1/files/{id}/reveal", func(w http.ResponseWriter, r *http.Request) {
		lf, err := n.Store.File(r.PathValue("id"))
		if err != nil {
			httpx.Error(w, http.StatusNotFound, "not_found", "file is not on this machine")
			return
		}
		if err := osutil.Reveal(lf.Path); err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"path": lf.Path})
	})
}
