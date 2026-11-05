// Package client is a Go client for the local control API (used by the CLI).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"netchat/internal/protocol"
	"netchat/internal/sse"
)

type Client struct {
	Base string // e.g. http://127.0.0.1:47801
	HTTP *http.Client
}

func New(apiAddr string) *Client {
	return &Client{Base: "http://" + apiAddr, HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("netchat is not running? (%w)", err)
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	if resp.StatusCode >= 300 {
		var e protocol.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error.Message != "" {
			return errors.New(e.Error.Message)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Machines() ([]protocol.Machine, error) {
	var out []protocol.Machine
	return out, c.do(http.MethodGet, "/v1/machines", nil, &out)
}

func (c *Client) AddPeer(addr string) (protocol.Info, error) {
	var out protocol.Info
	return out, c.do(http.MethodPost, "/v1/peers", map[string]string{"addr": addr}, &out)
}

func (c *Client) Scan() (int, error) {
	var out struct {
		Found int `json:"found"`
	}
	return out.Found, c.do(http.MethodPost, "/v1/scan", nil, &out)
}

func (c *Client) Identities() ([]protocol.Identity, error) {
	var out []protocol.Identity
	return out, c.do(http.MethodGet, "/v1/identities", nil, &out)
}

func (c *Client) CreateIdentity(nick, kind string) (protocol.Identity, error) {
	var out protocol.Identity
	return out, c.do(http.MethodPost, "/v1/identities", map[string]string{"nick": nick, "kind": kind}, &out)
}

func (c *Client) DeleteIdentity(sel string) (protocol.Identity, error) {
	var out protocol.Identity
	return out, c.do(http.MethodDelete, "/v1/identities/"+url.PathEscape(sel), nil, &out)
}

// DeleteMessage removes a message for everyone.
func (c *Client) DeleteMessage(id string) (protocol.Message, error) {
	var out protocol.Message
	return out, c.do(http.MethodDelete, "/v1/messages/"+url.PathEscape(id), nil, &out)
}

func (c *Client) Directory() ([]protocol.DirEntry, error) {
	var out []protocol.DirEntry
	return out, c.do(http.MethodGet, "/v1/directory", nil, &out)
}

func (c *Client) Channels() ([]protocol.ChannelInfo, error) {
	var out []protocol.ChannelInfo
	return out, c.do(http.MethodGet, "/v1/channels", nil, &out)
}

// Messages reads a channel: channel id, "#machine" or "@nick" (DM of `as`).
func (c *Client) Messages(channel, as string, afterSeq, beforeSeq int64, limit int) ([]protocol.Message, error) {
	q := url.Values{}
	if as != "" {
		q.Set("as", as)
	}
	if afterSeq > 0 {
		q.Set("after_seq", strconv.FormatInt(afterSeq, 10))
	}
	if beforeSeq > 0 {
		q.Set("before_seq", strconv.FormatInt(beforeSeq, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out []protocol.Message
	return out, c.do(http.MethodGet, "/v1/channels/"+url.PathEscape(channel)+"/messages?"+q.Encode(), nil, &out)
}

func (c *Client) Send(req protocol.SendRequest) (protocol.Message, error) {
	var out protocol.Message
	return out, c.do(http.MethodPost, "/v1/send", req, &out)
}

// Offer shares local files (by path) with a target.
func (c *Client) Offer(req protocol.SendRequest, paths []string) (protocol.Message, error) {
	body := struct {
		protocol.SendRequest
		Paths []string `json:"paths"`
	}{req, paths}
	var out protocol.Message
	return out, c.do(http.MethodPost, "/v1/offer", body, &out)
}

// Pull downloads an offered file; it blocks until the download finishes.
func (c *Client) Pull(fileID string) (protocol.LocalFile, error) {
	slow := *c
	slow.HTTP = &http.Client{} // no timeout: large files
	var out protocol.LocalFile
	return out, slow.do(http.MethodPost, "/v1/files/"+url.PathEscape(fileID)+"/pull", nil, &out)
}

func (c *Client) Files() ([]protocol.LocalFile, error) {
	var out []protocol.LocalFile
	return out, c.do(http.MethodGet, "/v1/files", nil, &out)
}

// Inbox long-polls for unacknowledged items (wait in seconds, max 60).
func (c *Client) Inbox(ctx context.Context, as, only string, limit int, wait time.Duration) ([]protocol.InboxItem, error) {
	q := url.Values{}
	q.Set("as", as)
	if only != "" {
		q.Set("only", only)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	q.Set("wait", strconv.FormatFloat(wait.Seconds(), 'f', -1, 64))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/v1/inbox?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: wait + 20*time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("netchat is not running? (%w)", err)
	}
	defer resp.Body.Close()
	var out []protocol.InboxItem
	return out, decode(resp, &out)
}

func (c *Client) Ack(as string, ids []string) (int, error) {
	var out struct {
		Acked int `json:"acked"`
	}
	return out.Acked, c.do(http.MethodPost, "/v1/ack", protocol.AckRequest{As: as, IDs: ids}, &out)
}

// Events streams /v1/events until ctx is done.
func (c *Client) Events(ctx context.Context, fn func(protocol.Event) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req) // no timeout: long-lived stream
	if err != nil {
		return fmt.Errorf("netchat is not running? (%w)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decode(resp, nil)
	}
	return sse.Read(resp.Body, func(name, _ string, data []byte) error {
		if name == "ping" {
			return nil
		}
		var e protocol.Event
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
		return fn(e)
	})
}
