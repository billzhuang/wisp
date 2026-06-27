// Command wisp is a Tailscale-native terminal: it embeds a userspace Tailscale
// node (tsnet) and dials SSH hosts on the tailnet with no Tailscale app, daemon,
// or system client installed. The terminal *is* the tailnet node.
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
	"syscall"

	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/config"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/billzhuang/wisp/internal/transport"
	"github.com/billzhuang/wisp/internal/update"
	"github.com/billzhuang/wisp/internal/version"
	"golang.org/x/crypto/ssh"
	xterm "golang.org/x/term"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Action flags that run without connecting.
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

	hostKey, err := buildHostKey(cfg)
	if err != nil {
		return err
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return err
	}

	ctrl, err := app.Dial(ctx, dialer, sshx.Config{
		Addr:    cfg.Addr(),
		User:    cfg.User,
		Auth:    auth,
		HostKey: hostKey,
	})
	if err != nil {
		return err
	}
	defer ctrl.Close()

	if cfg.Command != "" {
		if err := ctrl.StartCommand(cfg.Command); err != nil {
			return err
		}
	} else if err := ctrl.Start(); err != nil {
		return err
	}

	eng := terminal.DefaultEngine(80, 24)
	frontend := render.NewDefault()

	// If a newer release is pending and the frontend can show an in-app prompt
	// (the GUI), wire the click-to-install action — this is the Ghostty-style
	// "update available, click to install" affordance.
	if pendingUpdate != nil {
		if p, ok := frontend.(render.UpdatePrompter); ok {
			rel := pendingUpdate
			p.SetUpdate(fmt.Sprintf("Update %s available — press Ctrl+U to install", rel.Version()),
				func() error { return (&update.Applier{}).Apply(context.Background(), rel) })
		}
	}

	fmt.Fprintf(os.Stderr, "wisp: connected to %s (engine: %s)\n", cfg.Addr(), terminal.Backend)
	return frontend.Run(ctx, ctrl, eng)
}

func buildDialer(ctx context.Context, cfg *config.Config) (transport.Dialer, error) {
	if cfg.Direct {
		return transport.NewNetDialer(), nil
	}
	d, err := transport.NewTSNetDialer(transport.TSConfig{
		Hostname:   cfg.Hostname,
		StateDir:   cfg.StateDir,
		AuthKey:    cfg.AuthKey,
		ControlURL: cfg.ControlURL,
		Ephemeral:  cfg.Ephemeral,
		AuthLog:    os.Stderr, // surface the interactive login URL on first run
	})
	if err != nil {
		return nil, err
	}
	// Bring the node online before connecting so auth/login state surfaces
	// clearly rather than as an opaque dial timeout.
	if err := d.Up(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("bringing up tsnet node: %w", err)
	}
	return d, nil
}

func buildHostKey(cfg *config.Config) (ssh.HostKeyCallback, error) {
	if cfg.InsecureHostKey {
		fmt.Fprintln(os.Stderr, "wisp: WARNING host-key verification disabled")
		return sshx.InsecureIgnoreHostKey(), nil
	}
	// Trust on first use: record unknown hosts, reject changed keys.
	return sshx.KnownHostsCallback(cfg.KnownHosts, true)
}

func buildAuth(cfg *config.Config) ([]ssh.AuthMethod, error) {
	if cfg.IdentityFile != "" {
		key, err := os.ReadFile(cfg.IdentityFile)
		if err != nil {
			return nil, fmt.Errorf("reading identity file: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parsing identity file: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	// Interactive password.
	return []ssh.AuthMethod{ssh.PasswordCallback(func() (string, error) {
		fmt.Fprintf(os.Stderr, "%s@%s password: ", cfg.User, cfg.Host)
		pw, err := xterm.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(pw), err
	})}, nil
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
	applier := &update.Applier{}
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
