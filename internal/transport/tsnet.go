package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"tailscale.com/tsnet"
)

// TSConfig configures the embedded Tailscale node. This is the heart of the
// "no Tailscale app" property: the node identity, keys and netmap live entirely
// in StateDir, linked into our binary. There is no daemon, no system extension,
// and no `tailscale` CLI involved.
type TSConfig struct {
	// Hostname is the node name that appears in the tailnet.
	Hostname string

	// StateDir persists the node identity across runs. Required; if empty the
	// node would be ephemeral and re-authenticate every launch.
	StateDir string

	// AuthKey, if set, pre-authenticates the node (useful for ephemeral or
	// headless use). If empty, tsnet prints an interactive login URL on first
	// run, which we surface via AuthLog.
	AuthKey string

	// ControlURL overrides the coordination server. Empty uses Tailscale's
	// default (controlplane.tailscale.com); set this for Headscale or a
	// self-hosted/regional control plane.
	ControlURL string

	// Ephemeral registers the node as ephemeral (auto-removed shortly after it
	// disconnects). Typically paired with an ephemeral AuthKey.
	Ephemeral bool

	// AuthLog receives human-facing messages such as the interactive login URL.
	// If nil, those messages are discarded.
	AuthLog io.Writer
}

// TSNetDialer is a Dialer backed by an embedded tsnet node. Connections it
// returns ride userspace WireGuard (gVisor netstack) with direct + DERP
// fallback, exactly like a real Tailscale client, but with zero system
// dependencies.
type TSNetDialer struct {
	srv *tsnet.Server
}

// NewTSNetDialer constructs (but does not yet start) the embedded node. Call Up
// to bring it online, or rely on the lazy start inside Dial.
func NewTSNetDialer(cfg TSConfig) (*TSNetDialer, error) {
	if cfg.Hostname == "" {
		return nil, fmt.Errorf("transport: tsnet hostname is required")
	}
	if cfg.StateDir == "" {
		return nil, fmt.Errorf("transport: tsnet state dir is required (node identity must persist)")
	}
	srv := &tsnet.Server{
		Hostname:   cfg.Hostname,
		Dir:        cfg.StateDir,
		AuthKey:    cfg.AuthKey,
		ControlURL: cfg.ControlURL,
		Ephemeral:  cfg.Ephemeral,
	}
	if cfg.AuthLog != nil {
		srv.Logf = func(format string, args ...any) {
			// tsnet logs the interactive auth URL through Logf; forward it so
			// the user can complete login on first run.
			fmt.Fprintf(cfg.AuthLog, format+"\n", args...)
		}
	}
	return &TSNetDialer{srv: srv}, nil
}

// Up brings the node online and blocks until it has a usable netmap or ctx is
// done. It is optional — Dial starts the node lazily — but calling it lets the
// caller surface auth/login state before attempting a connection.
func (t *TSNetDialer) Up(ctx context.Context) error {
	_, err := t.srv.Up(ctx)
	return err
}

// Dial implements Dialer over the tsnet node. MagicDNS names (e.g. "dev-box:22")
// resolve through tsnet's own resolver, so no system DNS or hosts entry is
// needed.
func (t *TSNetDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	return t.srv.Dial(ctx, network, addr)
}

// Close shuts down the embedded node.
func (t *TSNetDialer) Close() error { return t.srv.Close() }
