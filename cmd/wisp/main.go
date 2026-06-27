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

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/config"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/billzhuang/wisp/internal/transport"
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
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
