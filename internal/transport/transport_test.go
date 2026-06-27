package transport

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestNetDialerRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c) // echo
	}()

	d := NewNetDialer()
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := d.Dial(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
}

func TestNetDialerImplementsDialer(t *testing.T) {
	var _ Dialer = NewNetDialer()
}

func TestTSNetDialerImplementsDialer(t *testing.T) {
	// Construct (do not start) a tsnet dialer and assert it satisfies the
	// interface. We can't bring a real node online in a unit test, but we can
	// verify config validation and the type contract.
	// Note: we deliberately do not start or Close the node here — tsnet's
	// Close panics if the server was never brought up, and a unit test cannot
	// reach the control plane to start it.
	d, err := NewTSNetDialer(TSConfig{Hostname: "wisp-test", StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	var _ Dialer = d
}

func TestTSNetDialerValidatesConfig(t *testing.T) {
	if _, err := NewTSNetDialer(TSConfig{StateDir: t.TempDir()}); err == nil {
		t.Fatal("expected error when hostname is empty")
	}
	if _, err := NewTSNetDialer(TSConfig{Hostname: "x"}); err == nil {
		t.Fatal("expected error when state dir is empty")
	}
}
