package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"netchat/internal/protocol"
)

// exitCode lets a command choose the process exit code.
type exitCode struct {
	code int
	msg  string
}

func (e exitCode) Error() string { return e.msg }

func inbox(args []string) error {
	f := newFlags("inbox")
	as := f.String("as", "", "local identity (default: first human)")
	wait := f.Float64("wait", 0, "seconds to wait for new messages when empty (max 60)")
	ack := f.Bool("ack", false, "acknowledge the returned items")
	only := f.String("only", "", "comma list of sources: mention,board (default all)")
	limit := f.Int("n", 50, "max items")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	items, err := c.Inbox(ctx, *as, *only, *limit, time.Duration(*wait*float64(time.Second)))
	if err != nil {
		return err
	}
	if *ack && len(items) > 0 {
		if _, err := c.Ack(*as, itemIDs(items)); err != nil {
			return err
		}
	}
	if *f.json {
		return printJSON(os.Stdout, items)
	}
	for _, it := range items {
		fmt.Printf("%-8s %s\n", it.Reason, formatLine(it.Message, ""))
	}
	return nil
}

func ackCmd(args []string) error {
	f := newFlags("ack")
	as := f.String("as", "", "local identity (default: first human)")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() == 0 {
		return errors.New("usage: netchat ack --as <me> <message-id...>")
	}
	n, err := c.Ack(*as, f.Args())
	if err != nil {
		return err
	}
	fmt.Printf("acked %d\n", n)
	return nil
}

// wait blocks until one message arrives, prints it, acks it. Exit code 3 on timeout.
func waitCmd(args []string) error {
	f := newFlags("wait")
	as := f.String("as", "", "local identity (default: first human)")
	timeout := f.Float64("timeout", 60, "give up after this many seconds (exit code 3); 0 = forever")
	only := f.String("only", "", "comma list of sources: mention,board (default all)")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var deadline time.Time
	if *timeout > 0 {
		deadline = time.Now().Add(time.Duration(*timeout * float64(time.Second)))
	}
	for ctx.Err() == nil {
		w := 60 * time.Second
		if !deadline.IsZero() {
			w = min(w, time.Until(deadline))
			if w <= 0 {
				break
			}
		}
		items, err := c.Inbox(ctx, *as, *only, 1, w)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			continue
		}
		it := items[0]
		if _, err := c.Ack(*as, []string{it.Message.ID}); err != nil {
			return err
		}
		if *f.json {
			return printJSON(os.Stdout, it)
		}
		fmt.Println(formatLine(it.Message, ""))
		return nil
	}
	return exitCode{3, "timeout: no message"}
}

// agent runs a hook command for every inbox message:
//   - the inbox item JSON is written to the hook's stdin
//   - env: NETCHAT_AS, NETCHAT_FROM, NETCHAT_REASON, NETCHAT_CHANNEL, NETCHAT_MESSAGE_ID, NETCHAT_TEXT, NETCHAT_API
//   - non-empty stdout is sent back as the reply, on the channel the message came from
//   - exit 0 acks the message; non-zero leaves it unacked and retries (max 3 tries)
func agentCmd(args []string) error {
	f := newFlags("agent")
	as := f.String("as", "", "local identity to act as (required)")
	hook := f.String("exec", "", "command to run per message (required)")
	only := f.String("only", "mention", "comma list of sources: mention,board")
	maxHops := f.Int("max-hops", 6, "do not reply to messages that already hopped this many times (agent loops)")
	timeout := f.Duration("timeout", 10*time.Minute, "hook timeout")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if *as == "" || *hook == "" {
		return errors.New(`usage: netchat agent --as <identity> --exec "<command>" [--only mention,board]`)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(os.Stderr, "netchat agent: %s listening (%s); Ctrl+C to stop\n", *as, *only)

	failures := map[string]int{}
	for ctx.Err() == nil {
		items, err := c.Inbox(ctx, *as, *only, 10, 60*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintln(os.Stderr, "inbox:", err, "(retrying in 3s)")
			sleep(ctx, 3*time.Second)
			continue
		}
		for _, it := range items {
			if ctx.Err() != nil {
				break
			}
			m := it.Message
			reply, err := runHook(ctx, *hook, *timeout, it, *as, *f.api)
			if err != nil {
				failures[m.ID]++
				fmt.Fprintf(os.Stderr, "hook failed for %s (try %d/3): %v\n", m.ID, failures[m.ID], err)
				if failures[m.ID] < 3 {
					// Re-poll; the message stays unacked. Later messages wait: acks are
					// per-channel cursors, so skipping ahead would lose this one.
					sleep(ctx, 2*time.Second)
					break
				}
				fmt.Fprintf(os.Stderr, "giving up on %s\n", m.ID)
			} else if reply != "" {
				switch {
				case m.Intent == protocol.IntentNotify:
					fmt.Fprintf(os.Stderr, "not replying to a notify message %s\n", m.ID)
				case m.Hops >= *maxHops:
					fmt.Fprintf(os.Stderr, "not replying to %s: hop limit %d reached\n", m.ID, *maxHops)
				default:
					if _, err := c.Send(protocol.SendRequest{As: *as, To: m.Channel, Text: reply,
						Intent: protocol.IntentAnswer, ReplyTo: m.ID, Hops: m.Hops + 1}); err != nil {
						fmt.Fprintf(os.Stderr, "reply to %s failed: %v\n", m.ID, err)
					}
				}
			}
			delete(failures, m.ID)
			if _, err := c.Ack(*as, []string{m.ID}); err != nil {
				fmt.Fprintln(os.Stderr, "ack:", err)
			}
		}
	}
	return nil
}

func runHook(ctx context.Context, hook string, timeout time.Duration, it protocol.InboxItem, as, apiAddr string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", hook)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", hook)
	}
	in, _ := json.Marshal(it)
	m := it.Message
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = append(os.Environ(),
		"NETCHAT_AS="+as,
		"NETCHAT_FROM="+m.From.Nick+"@"+m.FromMachineName,
		"NETCHAT_REASON="+it.Reason,
		"NETCHAT_CHANNEL="+m.Channel,
		"NETCHAT_MESSAGE_ID="+m.ID,
		"NETCHAT_TEXT="+m.Text,
		"NETCHAT_API="+apiAddr,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(out.String(), "\xef\xbb\xbf")), nil
}

func itemIDs(items []protocol.InboxItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.Message.ID
	}
	return ids
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
