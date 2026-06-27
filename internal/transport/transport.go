// Package transport abstracts how wisp reaches a remote host. The whole point
// of the project is that connectivity comes from an embedded userspace Tailscale
// node (tsnet) rather than a system Tailscale app or daemon — but the SSH layer
// and the tests must not care which dialer they were handed. That seam is the
// Dialer interface.
package transport

import (
	"context"
	"net"
)

// Dialer establishes a raw transport connection to "host:port". The SSH client
// is layered on top of whatever net.Conn this returns, so the same SSH code
// works over a tsnet WireGuard tunnel, a plain TCP socket (tests), or anything
// else that satisfies this interface.
type Dialer interface {
	// Dial opens a connection to addr ("host:port"). The network is "tcp".
	Dial(ctx context.Context, network, addr string) (net.Conn, error)

	// Close releases any resources held by the dialer (e.g. the tsnet node).
	Close() error
}

// NetDialer is a Dialer backed by the host's own network stack. It is used by
// tests and by a `--direct` escape hatch; it deliberately does NOT touch
// Tailscale, so it must only be pointed at directly reachable hosts.
type NetDialer struct {
	d net.Dialer
}

// NewNetDialer returns a Dialer that uses the OS network stack directly.
func NewNetDialer() *NetDialer { return &NetDialer{} }

// Dial implements Dialer.
func (n *NetDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return n.d.DialContext(ctx, network, addr)
}

// Close implements Dialer. The OS dialer holds nothing to release.
func (n *NetDialer) Close() error { return nil }
