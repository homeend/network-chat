package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"netchat/internal/client"
	"netchat/internal/protocol"
)

func who(args []string) error {
	f := newFlags("who")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	ms, err := c.Machines()
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, ms)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MACHINE\tADDR\tSTATUS\tSEEN\tVIA\tUSERS\tID")
	for _, m := range ms {
		status, seen, addr := "offline", ago(m.LastSeen), m.Addr
		if m.Online {
			status = "online"
		}
		if m.Self {
			addr, seen = "(this machine)", "-"
		}
		var nicks []string
		for _, i := range m.Identities {
			nicks = append(nicks, nickLabel(i))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Name, addr, status, seen, dash(m.Via), dash(strings.Join(nicks, " ")), short(m.InstanceID))
	}
	return tw.Flush()
}

func nickLabel(i protocol.IdentityRef) string {
	if i.Kind == protocol.KindAgent {
		return i.Nick + "[agent]"
	}
	return i.Nick
}

func add(args []string) error {
	f := newFlags("add")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: netchat add <host[:port]>")
	}
	info, err := c.AddPeer(f.Arg(0))
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, info)
	}
	fmt.Printf("added %s (%s)\n", info.Machine, short(info.InstanceID))
	return nil
}

func scan(args []string) error {
	f := newFlags("scan")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	n, err := c.Scan()
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, map[string]int{"found": n})
	}
	fmt.Printf("scan found %d instance(s)\n", n)
	return nil
}

func users(args []string) error {
	f := newFlags("users")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	dir, err := c.Directory()
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, dir)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tKIND\tSTATUS\tID")
	for _, e := range dir {
		status := "offline"
		if e.Online {
			status = "online"
		}
		if e.Local {
			status = "local"
		}
		fmt.Fprintf(tw, "%s@%s\t%s\t%s\t%s\n", e.Nick, e.MachineName, e.Kind, status, e.ID)
	}
	return tw.Flush()
}

func identity(args []string) error {
	sub := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	f := newFlags("id " + sub)
	agent := f.Bool("agent", false, "create an agent identity")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	switch sub {
	case "ls", "list":
		list, err := c.Identities()
		if err != nil {
			return err
		}
		if *f.json {
			return printJSON(os.Stdout, list)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NICK\tKIND\tID")
		for i, id := range list {
			def := ""
			if i == firstHuman(list) {
				def = "  (default)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s%s\n", id.Nick, id.Kind, id.ID, def)
		}
		return tw.Flush()
	case "new", "add":
		if f.NArg() != 1 {
			return errors.New("usage: netchat id new <nick> [--agent]")
		}
		kind := protocol.KindHuman
		if *agent {
			kind = protocol.KindAgent
		}
		id, err := c.CreateIdentity(f.Arg(0), kind)
		if err != nil {
			return err
		}
		if *f.json {
			return printJSON(os.Stdout, id)
		}
		fmt.Printf("created %s (%s) %s\n", id.Nick, id.Kind, id.ID)
		return nil
	case "rm", "del", "delete":
		if f.NArg() != 1 {
			return errors.New("usage: netchat id rm <nick|id>")
		}
		id, err := c.DeleteIdentity(f.Arg(0))
		if err != nil {
			return err
		}
		fmt.Printf("deleted %s\n", id.Nick)
		return nil
	}
	return fmt.Errorf("unknown: netchat id %s", sub)
}

func firstHuman(list []protocol.Identity) int {
	for i, id := range list {
		if id.Kind == protocol.KindHuman {
			return i
		}
	}
	return 0
}

func sendCmd(args []string) error {
	f := newFlags("send")
	as := f.String("as", "", "send as this local identity (default: first human)")
	intent := f.String("intent", "", "ask | answer | notify")
	replyTo := f.String("reply-to", "", "message id this replies to")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() < 2 {
		return errors.New("usage: netchat send [--as me] <#machine> <text...|->   (@nick in the text notifies someone)")
	}
	text := strings.Join(f.Args()[1:], " ")
	if text == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, protocol.MaxTextBytes+1))
		if err != nil {
			return err
		}
		text = strings.TrimRight(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\r\n") // PowerShell pipes a BOM
	}
	m, err := c.Send(protocol.SendRequest{As: *as, To: f.Arg(0), Text: text, Intent: *intent, ReplyTo: *replyTo})
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, m)
	}
	fmt.Printf("sent %s seq=%d id=%s\n", m.Channel, m.Seq, m.ID)
	return nil
}

func read(args []string) error {
	f := newFlags("read")
	limit := f.Int("n", 20, "number of messages")
	after := f.Int64("after", 0, "only messages with seq > after")
	showIDs := f.Bool("ids", false, "show message ids (for netchat rm)")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: netchat read <#machine|channel-id>")
	}
	ms, err := c.Messages(f.Arg(0), *after, 0, *limit)
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, ms)
	}
	for _, m := range ms {
		if *showIDs {
			fmt.Print(m.ID, " ")
		}
		fmt.Println(formatLine(m, ""))
	}
	return nil
}

func rmCmd(args []string) error {
	f := newFlags("rm")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() == 0 {
		return errors.New("usage: netchat rm <message-id...>   (ids: netchat read <channel> --ids)")
	}
	for _, id := range f.Args() {
		m, err := c.DeleteMessage(id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if *f.json {
			if err := printJSON(os.Stdout, m); err != nil {
				return err
			}
		} else {
			fmt.Println("removed", id)
		}
	}
	return nil
}

func channels(args []string) error {
	f := newFlags("channels")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	cs, err := c.Channels()
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, cs)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHANNEL\tSTATUS\tMESSAGES\tLAST\tID")
	for _, ch := range cs {
		status, last := "offline", "-"
		if ch.Online {
			status = "online"
		}
		if !ch.LastTS.IsZero() {
			last = ago(ch.LastTS) + " ago"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", ch.Name, status, ch.LastSeq, last, ch.ID)
	}
	return tw.Flush()
}

func tail(args []string) error {
	f := newFlags("tail")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	names := machineNames(c)
	err = c.Events(ctx, func(e protocol.Event) error {
		if e.Type != "message" || e.Message == nil {
			return nil
		}
		if *f.json {
			return printJSON(os.Stdout, e.Message)
		}
		where := ""
		if id, ok := strings.CutPrefix(e.Message.Channel, "board:"); ok {
			if _, known := names[id]; !known {
				names = machineNames(c)
			}
			where = "#" + names[id] + " "
		}
		fmt.Println(formatLine(*e.Message, where))
		return nil
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func machineNames(c *client.Client) map[string]string {
	out := map[string]string{}
	ms, _ := c.Machines()
	for _, m := range ms {
		out[m.InstanceID] = m.Name
	}
	return out
}

func formatLine(m protocol.Message, where string) string {
	from := fmt.Sprintf("%s@%s", nickLabel(m.From), m.FromMachineName)
	text := m.Text
	if m.Deleted {
		text = "(message removed)"
	}
	line := fmt.Sprintf("[%s] %s<%s> %s", m.TS.Local().Format("15:04:05"), where, from, text)
	for _, f := range m.Files {
		line += fmt.Sprintf("\n    [file] %s (%s) id=%s", f.Name, humanSize(f.Size), f.ID)
	}
	return line
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func offer(args []string) error {
	f := newFlags("offer")
	as := f.String("as", "", "send as this local identity (default: first human)")
	text := f.String("text", "", "message text to go with the files")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() < 2 {
		return errors.New("usage: netchat offer [--as me] [--text ...] <#machine> <path...>")
	}
	m, err := c.Offer(protocol.SendRequest{As: *as, To: f.Arg(0), Text: *text}, f.Args()[1:])
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, m)
	}
	for _, x := range m.Files {
		fmt.Printf("offered %s (%s) id=%s\n", x.Name, humanSize(x.Size), x.ID)
	}
	return nil
}

func pull(args []string) error {
	f := newFlags("pull")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.NArg() < 1 {
		return errors.New("usage: netchat pull <file-id...>")
	}
	var out []protocol.LocalFile
	for _, id := range f.Args() {
		lf, err := c.Pull(id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, lf)
		if !*f.json {
			fmt.Println(lf.Path)
		}
	}
	if *f.json {
		return printJSON(os.Stdout, out)
	}
	return nil
}

func files(args []string) error {
	f := newFlags("files")
	c, err := f.parse(args)
	if err != nil {
		return err
	}
	fs, err := c.Files()
	if err != nil {
		return err
	}
	if *f.json {
		return printJSON(os.Stdout, fs)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tSIZE\tKIND\tPATH\tID")
	for _, x := range fs {
		kind := "downloaded"
		if x.Owned {
			kind = "offered"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", x.Name, humanSize(x.Size), kind, x.Path, x.ID)
	}
	return tw.Flush()
}
