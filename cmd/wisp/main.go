// Command wisp is a Tailscale-native terminal: it embeds a userspace Tailscale
// node (tsnet) and runs a local shell whose network egress is routed through the
// tailnet — with no Tailscale app, daemon, or system client installed, and
// without touching the host's DNS or routing. Tools run in the shell (curl, git,
// Claude Code, Codex, …) reach tailnet and subnet-router resources by way of an
// embedded proxy whose address is injected into the shell's environment.
//
// The default build renders through the local OS terminal (stdio frontend);
// build with `-tags ebiten` for the GPU window frontend and `-tags libghostty`
// for the libghostty VT engine (see docs/BUILD.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/banner"
	"github.com/billzhuang/wisp/internal/config"
	"github.com/billzhuang/wisp/internal/localpty"
	"github.com/billzhuang/wisp/internal/proxy"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/billzhuang/wisp/internal/transport"
	"github.com/billzhuang/wisp/internal/update"
	"github.com/billzhuang/wisp/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "wisp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Parse(args, os.Getenv)
	if err != nil {
		return err
	}

	// Reclaim any update artifacts (the previous ".old" binary, interrupted
	// download temp files) left next to our executable, so repeated self-updates
	// don't accumulate disk. Best-effort: never blocks or fails the launch.
	if n := (&update.Applier{Prefix: assetFlavor}).CleanupLeftovers(); n > 0 {
		fmt.Fprintf(os.Stderr, "wisp: reclaimed %d leftover update file(s)\n", n)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Action flags that run without starting a terminal.
	if cfg.ShowVersion {
		fmt.Printf("wisp %s (engine: %s)\n", version.Current(), terminal.Backend)
		return nil
	}
	if cfg.DoUpdate {
		return runUpdate(ctx)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	// ASCII splash (constant string — no measurable startup cost).
	fmt.Fprint(os.Stderr, banner.Render(version.Current(), "starting tailnet node …"))

	var pendingUpdate *update.Release
	if !cfg.NoUpdateCheck {
		pendingUpdate = checkForUpdate(ctx)
		if pendingUpdate != nil {
			fmt.Fprintf(os.Stderr, "wisp: update available %s -> %s (run `wisp -update`)\n",
				version.Current(), pendingUpdate.Version())
		}
	}

	dialer, err := buildDialer(ctx, cfg)
	if err != nil {
		return err
	}
	defer dialer.Close()

	// Start the embedded egress proxy: connections it accepts are dialed through
	// the tailnet node. The shell reaches tailnet resources by pointing its
	// proxy env vars here.
	px, err := proxy.Start(cfg.ProxyAddr, dialer)
	if err != nil {
		return fmt.Errorf("starting tailnet proxy: %w", err)
	}
	defer px.Close()

	childEnv := proxyEnv(os.Environ(), px.Addr())

	ctrl := app.NewLocal(shellConfig(cfg, childEnv))
	defer ctrl.Close()
	if err := ctrl.Start(); err != nil {
		return err
	}

	eng := terminal.DefaultEngine(80, 24)
	frontend := render.NewDefault()

	// A tab-capable frontend (the GUI) drives several concurrent sessions as
	// tabs. Wrap the initial session plus an opener that spawns another local
	// shell (sharing the same node + proxy) into a tab manager; every other
	// frontend keeps the single session.
	var rctrl render.Controller = ctrl
	if tc, ok := frontend.(render.TabCapable); ok && tc.SupportsTabs() {
		open := func() (render.Controller, terminal.Engine, func() error, error) {
			c := app.NewLocal(shellConfig(cfg, childEnv))
			if err := c.Start(); err != nil {
				c.Close()
				return nil, nil, nil, err
			}
			cols, rows := eng.Size()
			return c, terminal.DefaultEngine(cols, rows), c.Close, nil
		}
		tabs := app.NewTabs(ctrl, eng, ctrl.Close, open)
		defer tabs.Close()
		rctrl = tabs
	}

	// If a newer release is pending and the frontend can show an in-app prompt
	// (the GUI), wire the click-to-install action.
	if pendingUpdate != nil {
		if p, ok := frontend.(render.UpdatePrompter); ok {
			rel := pendingUpdate
			p.SetUpdate(fmt.Sprintf("Update %s available — press Ctrl+U to install", rel.Version()),
				func() error { return (&update.Applier{Prefix: assetFlavor}).Apply(ctx, rel) })
		}
	}

	fmt.Fprintf(os.Stderr, "wisp: tailnet proxy on %s (engine: %s)\n", px.Addr(), terminal.Backend)
	return frontend.Run(ctx, rctrl, eng)
}

// buildDialer returns the transport the proxy dials through: the embedded tsnet
// node by default, or the OS network stack under -no-tailnet.
func buildDialer(ctx context.Context, cfg *config.Config) (transport.Dialer, error) {
	if cfg.NoTailnet {
		return transport.NewNetDialer(), nil
	}
	d, err := transport.NewTSNetDialer(transport.TSConfig{
		Hostname:     cfg.Hostname,
		StateDir:     cfg.StateDir,
		AuthKey:      cfg.AuthKey,
		ClientSecret: cfg.ClientSecret,
		Tags:         cfg.Tags,
		ControlURL:   cfg.ControlURL,
		Ephemeral:    cfg.Ephemeral,
		AuthLog:      os.Stderr, // surface the interactive login URL on first run
	})
	if err != nil {
		return nil, err
	}
	// Bring the node online before serving so auth/login state surfaces clearly
	// rather than as an opaque proxy-dial timeout later.
	if err := d.Up(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("bringing up tsnet node: %w", err)
	}
	return d, nil
}

// shellConfig builds the localpty config for the shell wisp launches, under the
// given (proxy-augmented) environment.
func shellConfig(cfg *config.Config, env []string) localpty.Config {
	shell := cfg.ResolveShell(os.Getenv)
	lc := localpty.Config{
		Path: shell,
		Env:  env,
		Term: "xterm-256color",
		Cols: 80, Rows: 24,
	}
	if cfg.Command != "" {
		lc.Args = []string{shell, "-c", cfg.Command}
	} else {
		// argv[0] of "-<base>" requests a login shell, so the user's profile is
		// sourced (PATH, etc.) just as a normal terminal login would.
		lc.Args = []string{"-" + filepath.Base(shell)}
	}
	return lc
}

// proxyEnv returns base with the standard proxy environment variables pointed at
// the embedded proxy (overriding any inherited values), plus a WISP marker so a
// shell prompt or script can tell it is running inside wisp.
func proxyEnv(base []string, addr string) []string {
	httpURL := "http://" + addr
	socksURL := "socks5h://" + addr
	out := append([]string(nil), base...)
	for _, kv := range [][2]string{
		{"HTTP_PROXY", httpURL}, {"http_proxy", httpURL},
		{"HTTPS_PROXY", httpURL}, {"https_proxy", httpURL},
		{"ALL_PROXY", socksURL}, {"all_proxy", socksURL},
		{"WISP", "1"},
	} {
		out = setEnv(out, kv[0], kv[1])
	}
	return out
}

// setEnv replaces KEY=… in env if present, otherwise appends it.
func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

// runUpdate performs the click-to-install flow (here, the CLI form): check for a
// newer release and, if found, download + verify + replace the binary in place.
func runUpdate(ctx context.Context) error {
	checker := &update.Checker{Repo: version.Repo, Current: version.Current()}
	rel, newer, err := checker.CheckForUpdate(ctx)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !newer {
		fmt.Printf("wisp %s is the latest version.\n", version.Current())
		return nil
	}
	fmt.Printf("Updating wisp %s -> %s ...\n", version.Current(), rel.Version())
	applier := &update.Applier{Prefix: assetFlavor}
	if err := applier.Apply(ctx, rel); err != nil {
		return fmt.Errorf("installing update: %w", err)
	}
	fmt.Printf("Updated to %s. Restart wisp to run the new version.\n", rel.Version())
	if rel.HTMLURL != "" {
		fmt.Printf("Release notes: %s\n", rel.HTMLURL)
	}
	return nil
}

// checkForUpdate does a quick, best-effort check and returns the newer release
// if one exists. It never blocks startup for more than a couple of seconds and
// never fails the launch (errors and dev builds yield nil).
func checkForUpdate(ctx context.Context) *update.Release {
	if version.IsDev() {
		return nil // local builds have no meaningful "latest" to compare against
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	checker := &update.Checker{Repo: version.Repo, Current: version.Current()}
	rel, newer, err := checker.CheckForUpdate(cctx)
	if err != nil || !newer {
		return nil
	}
	return rel
}
