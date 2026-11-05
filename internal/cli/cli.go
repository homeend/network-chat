// Package cli implements the netchat subcommands.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"netchat/internal/client"
	"netchat/internal/protocol"
)

const usage = `netchat — LAN chat for humans and agents

Service:
  netchat [flags]                  tray icon + service, opens the UI in your browser
                                   (if already running: just opens the UI)
  netchat serve [flags]            headless service (no tray, no browser)

Network:
  netchat who                      machines on the network
  netchat users                    all addressable identities
  netchat add <host[:port]>        add a peer by address (saved as favorite)
  netchat scan                     probe local /24 subnets
  netchat doctor                   network setup check (interfaces, WSL, firewall hints)

Identities (on this machine):
  netchat id                       list
  netchat id new <nick> [--agent]  create
  netchat id rm <nick|id>          delete

Messages:
  netchat send [--as me] <to> <text...|->   to: #machine, #, @nick, nick@machine
  netchat read [--as me] <channel> [-n 20] [--ids]  channel: #machine, @nick, channel id
  netchat rm <message-id...>       remove for everyone (own messages, or any on this
                                   machine's channel; the holder must be online)
  netchat channels                 machine channels and conversations
  netchat tail                     stream new messages

Files (offer + pull; bytes stay on the sender until pulled):
  netchat offer [--text ..] <to> <path...>  share files with #machine or @nick
  netchat pull <file-id...>        download into ~/Downloads/netchat, prints paths
  netchat files                    files offered by / downloaded to this machine

Agents (each identity has its own inbox; reading does not acknowledge):
  netchat inbox --as bot [--wait 30] [--ack] [--only dm,mention,board]
  netchat ack --as bot <message-id...>
  netchat wait --as bot [--timeout 60]      block for one message, print + ack it
                                            (exit code 3 on timeout)
  netchat agent --as bot --exec "<cmd>"     run <cmd> per message: item JSON on
                                            stdin, stdout is sent as the reply,
                                            exit 0 acks (see README)

Teach coding agents (Claude Code, Codex, Junie, Kimi, Gemini, Copilot, Cursor, OpenCode)
to use netchat by installing a skill (also in the UI: ⚙ Settings):
  netchat skills                    status per agent
  netchat skills install detected   install/update for detected agents (or: claude codex ...)
  netchat skills remove <agent|all>

Every client command takes --api (env NETCHAT_API) and --json.
`

func Run(args []string) int {
	cmd := "app"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	cmds := map[string]func([]string) error{
		"app": app, "serve": serve, "who": who, "add": add, "scan": scan, "users": users,
		"id": identity, "send": sendCmd, "read": read, "rm": rmCmd, "channels": channels, "tail": tail,
		"offer": offer, "pull": pull, "files": files,
		"inbox": inbox, "ack": ackCmd, "wait": waitCmd, "agent": agentCmd, "skills": skillsCmd, "doctor": doctor,
	}
	var err error
	switch f, ok := cmds[cmd]; {
	case ok:
		err = f(args)
	case cmd == "version":
		fmt.Println(protocol.Version)
	case cmd == "help" || cmd == "-h" || cmd == "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		var ec exitCode
		if errors.As(err, &ec) {
			fmt.Fprintln(os.Stderr, ec.msg)
			return ec.code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func defaultAPI() string {
	if v := os.Getenv("NETCHAT_API"); v != "" {
		return v
	}
	return fmt.Sprintf("127.0.0.1:%d", protocol.DefaultAPIPort)
}

// cmdFlags is a flag set with the common client flags.
type cmdFlags struct {
	*flag.FlagSet
	api  *string
	json *bool
}

func newFlags(name string) cmdFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return cmdFlags{FlagSet: fs,
		api:  fs.String("api", defaultAPI(), "local control API address"),
		json: fs.Bool("json", false, "JSON output")}
}

func (f cmdFlags) parse(args []string) (*client.Client, error) {
	if err := parseInterspersed(f.FlagSet, args); err != nil {
		return nil, err
	}
	return client.New(*f.api), nil
}

// parseInterspersed allows flags after positional args (`add host --json`).
// Positionals end up in fs.Args().
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if rest[0] == "--" {
			pos = append(pos, rest[1:]...)
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return fs.Parse(append([]string{"--"}, pos...))
}

func printJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func ago(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < time.Hour {
		return d.String()
	}
	return d.Round(time.Minute).String()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
