package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"netchat/internal/api"
	"netchat/internal/config"
	"netchat/internal/node"
	"netchat/internal/osutil"
	"netchat/internal/peer"
	"netchat/internal/protocol"
	"netchat/internal/store"
	"netchat/internal/tray"
)

type serveOpts struct {
	cfg     config.Config
	verbose bool
	noOpen  bool
}

func parseServe(name string, args []string) (serveOpts, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var o serveOpts
	c := &o.cfg
	fs.StringVar(&c.DataDir, "data", "", "data directory (default: user config dir/netchat)")
	fs.StringVar(&c.Name, "name", "", "machine display name (default: hostname)")
	fs.IntVar(&c.Port, "port", protocol.DefaultPort, "LAN port (UDP beacons + HTTP)")
	fs.IntVar(&c.HTTPPort, "http-port", 0, "LAN HTTP port (default: same as --port; for running several instances on one host)")
	fs.StringVar(&c.APIAddr, "api", defaultAPI(), "local control API / UI address")
	fs.StringVar(&c.Group, "group", protocol.DefaultGroup, "multicast group")
	fs.BoolVar(&c.AllIfaces, "all-interfaces", false, "include virtual/VPN adapters")
	fs.StringVar(&c.Downloads, "downloads", "", "where pulled files are saved (default: ~/Downloads/netchat)")
	fs.BoolVar(&o.verbose, "v", false, "debug logging")
	if name == "app" {
		fs.BoolVar(&o.noOpen, "no-open", false, "do not open the browser on start")
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	cfg, err := config.Load(o.cfg)
	if err != nil {
		return o, err
	}
	if h, _, err := net.SplitHostPort(cfg.APIAddr); err != nil || !net.ParseIP(h).IsLoopback() {
		return o, fmt.Errorf("--api must be a loopback address, got %q", cfg.APIAddr)
	}
	o.cfg = cfg
	return o, nil
}

func newLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// serve runs headless (no tray, no browser): for servers and agent hosts.
func serve(args []string) error {
	o, err := parseServe("serve", args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runService(ctx, o.cfg, newLogger(os.Stderr, o.verbose), nil)
}

// app is the default (double-click) mode: service + tray icon + browser UI.
// If netchat already runs on this machine it only opens the UI.
func app(args []string) error {
	o, err := parseServe("app", args)
	if err != nil {
		return err
	}
	uiURL := "http://" + o.cfg.APIAddr + "/"
	if alreadyRunning(o.cfg.APIAddr) {
		return osutil.OpenURL(uiURL)
	}
	hidden := osutil.HideConsoleIfOwned()

	logw := io.Writer(os.Stderr)
	if f, err := openLog(o.cfg.DataDir); err == nil {
		defer f.Close()
		if hidden {
			logw = f
		} else {
			logw = io.MultiWriter(os.Stderr, f)
		}
	}
	log := newLogger(logw, o.verbose)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		err := runService(ctx, o.cfg, log, ready)
		done <- err
		tray.Stop()
	}()
	go func() {
		select {
		case <-ready:
			if !o.noOpen {
				_ = osutil.OpenURL(uiURL)
			}
		case <-ctx.Done():
		}
	}()

	if tray.Supported {
		tray.Run(tray.Options{
			Tooltip: "netchat · " + o.cfg.Name,
			Machine: o.cfg.Name,
			OnOpen:  func() { _ = osutil.OpenURL(uiURL) },
			OnQuit:  cancel,
		})
	} else {
		<-ctx.Done()
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
	}
	if err != nil && hidden {
		osutil.Alert("netchat", err.Error())
	}
	return err
}

func alreadyRunning(apiAddr string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get("http://" + apiAddr + "/v1/info")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func openLog(dir string) (*os.File, error) {
	path := filepath.Join(dir, "netchat.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// runService runs the node and both HTTP servers until ctx is done.
// ready (optional) is closed once the listeners are up.
func runService(ctx context.Context, cfg config.Config, log *slog.Logger, ready chan<- struct{}) error {
	st, err := store.Open(filepath.Join(cfg.DataDir, "netchat.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := node.New(cfg, log, st)
	if err != nil {
		return err
	}

	lanLn, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort))
	if err != nil {
		return fmt.Errorf("LAN port %d is busy (is another netchat running? use --http-port): %w", cfg.HTTPPort, err)
	}
	apiLn, err := net.Listen("tcp", cfg.APIAddr)
	if err != nil {
		lanLn.Close()
		return fmt.Errorf("local API %s is busy (use --api): %w", cfg.APIAddr, err)
	}
	// Two separate servers so a control route can never be exposed on the LAN.
	// BaseContext ends long-lived SSE handlers on shutdown.
	base := func(net.Listener) context.Context { return ctx }
	lanSrv := &http.Server{Handler: peer.Handler(n), ReadHeaderTimeout: 10 * time.Second, BaseContext: base}
	apiSrv := &http.Server{Handler: api.Handler(n), ReadHeaderTimeout: 10 * time.Second, BaseContext: base}
	go func() { _ = lanSrv.Serve(lanLn) }()
	go func() { _ = apiSrv.Serve(apiLn) }()

	log.Info("netchat started", "machine", cfg.Name, "instance", cfg.InstanceID[:8],
		"lan", lanLn.Addr().String(), "ui", "http://"+apiLn.Addr().String()+"/", "data", cfg.DataDir)
	if ready != nil {
		close(ready)
	}
	for _, line := range osutil.WSLAdvice(osutil.DetectWSL(), localIPs(cfg)) {
		log.Warn(line)
	}

	runErr := n.Run(ctx)
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = lanSrv.Shutdown(shutCtx)
	_ = apiSrv.Shutdown(shutCtx)
	if ctx.Err() != nil {
		log.Info("netchat stopped")
		return nil
	}
	return runErr
}
