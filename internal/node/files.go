package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"netchat/internal/config"
	"netchat/internal/protocol"
	"netchat/internal/store"
)

var (
	ErrGone   = errors.New("file no longer available")
	ErrBusy   = errors.New("download already in progress")
	pulls     sync.Map // file id -> struct{} while downloading
	reserved  = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	badInName = `<>:"/\|?*`
)

// OfferPath registers a local file (it stays where it is) and returns its ref.
func (n *Node) OfferPath(path string) (protocol.FileRef, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return protocol.FileRef{}, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return protocol.FileRef{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return protocol.FileRef{}, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return protocol.FileRef{}, err
	}
	return n.registerOffer(filepath.Base(abs), st.Size(), hex.EncodeToString(h.Sum(nil)), abs, "")
}

// OfferUpload stores uploaded bytes (UI drag & drop) in the data dir outbox
// and registers them as an offer.
func (n *Node) OfferUpload(name, mimeType string, r io.Reader) (protocol.FileRef, error) {
	dir := filepath.Join(n.Cfg.DataDir, "outbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return protocol.FileRef{}, err
	}
	tmp, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return protocol.FileRef{}, err
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return protocol.FileRef{}, err
	}
	return n.registerOffer(name, size, hex.EncodeToString(h.Sum(nil)), tmp.Name(), mimeType)
}

func (n *Node) registerOffer(name string, size int64, sum, path, mimeType string) (protocol.FileRef, error) {
	name = SafeFileName(name)
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ref := protocol.FileRef{ID: config.RandomID(16), Name: name, Size: size, Mime: mimeType, SHA256: sum, Machine: n.Cfg.InstanceID}
	return ref, n.Store.PutFile(protocol.LocalFile{FileRef: ref, Owned: true, Path: path, CreatedAt: time.Now()})
}

// OwnedFile returns an offer of ours that peers may download.
func (n *Node) OwnedFile(id string) (protocol.LocalFile, error) {
	f, err := n.Store.File(id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !f.Owned) {
		return f, fmt.Errorf("%w: no such file", ErrNotFound)
	}
	return f, err
}

// Pull downloads a file offered by another machine into the downloads
// directory (resumable via Range, verified by SHA-256). It blocks until done.
func (n *Node) Pull(ctx context.Context, id string) (protocol.LocalFile, error) {
	if lf, err := n.Store.File(id); err == nil {
		if _, serr := os.Stat(lf.Path); serr == nil {
			return lf, nil // ours, or already downloaded
		}
	}
	ref, err := n.Store.FindFileRef(id)
	if err != nil {
		return protocol.LocalFile{}, fmt.Errorf("%w: unknown file %s", ErrNotFound, id)
	}
	if _, busy := pulls.LoadOrStore(id, struct{}{}); busy {
		return protocol.LocalFile{}, ErrBusy
	}
	defer pulls.Delete(id)

	lf, err := n.download(ctx, ref)
	st := protocol.FileStatus{ID: id, State: "done", Bytes: ref.Size, Total: ref.Size, Path: lf.Path}
	if err != nil {
		st = protocol.FileStatus{ID: id, State: "error", Total: ref.Size, Error: err.Error()}
	}
	n.Bus.Publish(protocol.Event{Type: "file", File: &st})
	return lf, err
}

func (n *Node) download(ctx context.Context, ref protocol.FileRef) (protocol.LocalFile, error) {
	p, ok := n.Peers.Get(ref.Machine)
	if !ok || !p.Online(time.Now()) {
		return protocol.LocalFile{}, fmt.Errorf("%w: the machine offering this file is not reachable", ErrOffline)
	}
	if err := os.MkdirAll(n.Cfg.Downloads, 0o755); err != nil {
		return protocol.LocalFile{}, err
	}
	part := filepath.Join(n.Cfg.Downloads, ".netchat-"+ref.ID+".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return protocol.LocalFile{}, err
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	offset, _ := f.Seek(0, io.SeekEnd)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+p.Addr()+"/p/files/"+ref.ID, nil)
	if err != nil {
		return protocol.LocalFile{}, err
	}
	if offset > 0 && offset < ref.Size {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		req.Header.Set("If-Range", `"`+ref.SHA256+`"`)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return protocol.LocalFile{}, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK: // full body: start over
		if err := f.Truncate(0); err != nil {
			return protocol.LocalFile{}, err
		}
		offset, _ = f.Seek(0, io.SeekStart)
	case http.StatusRequestedRangeNotSatisfiable:
		if offset != ref.Size {
			f.Truncate(0)
			return protocol.LocalFile{}, fmt.Errorf("resume failed, try again")
		}
	case http.StatusNotFound, http.StatusGone:
		return protocol.LocalFile{}, fmt.Errorf("%w: the sender no longer offers it", ErrGone)
	default:
		return protocol.LocalFile{}, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		pw := &progressWriter{n: n, id: ref.ID, done: offset, total: ref.Size}
		if _, err := io.Copy(io.MultiWriter(f, pw), resp.Body); err != nil {
			return protocol.LocalFile{}, fmt.Errorf("download interrupted (will resume): %w", err)
		}
	}

	// Verify the whole file.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return protocol.LocalFile{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return protocol.LocalFile{}, err
	}
	f.Close()
	f = nil
	if got := hex.EncodeToString(h.Sum(nil)); got != ref.SHA256 || size != ref.Size {
		os.Remove(part)
		return protocol.LocalFile{}, fmt.Errorf("checksum mismatch (the file changed on the sender?)")
	}
	final := uniquePath(n.Cfg.Downloads, SafeFileName(ref.Name))
	if err := os.Rename(part, final); err != nil {
		return protocol.LocalFile{}, err
	}
	lf := protocol.LocalFile{FileRef: ref, Owned: false, Path: final, CreatedAt: time.Now()}
	n.Log.Info("file downloaded", "name", ref.Name, "path", final, "bytes", size)
	return lf, n.Store.PutFile(lf)
}

type progressWriter struct {
	n           *Node
	id          string
	done, total int64
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 250*time.Millisecond {
		p.last = time.Now()
		p.n.Bus.Publish(protocol.Event{Type: "file", File: &protocol.FileStatus{ID: p.id, State: "downloading", Bytes: p.done, Total: p.total}})
	}
	return len(b), nil
}

// SafeFileName turns an untrusted name into a plain file name that is safe on
// every OS (no paths, no reserved names, no control characters).
func SafeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(badInName, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "file"
	}
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	if reserved[base] || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
		name = "_" + name
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}

func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}
