package proxy

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/transport"
)

// echoServer starts a TCP echo server on loopback and returns its address.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

// startProxy starts a proxy backed by the OS network stack (NetDialer), the
// same transport -no-tailnet uses, so the test is hermetic.
func startProxy(t *testing.T) *Server {
	t.Helper()
	d := transport.NewNetDialer()
	t.Cleanup(func() { d.Close() })
	px, err := Start("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { px.Close() })
	return px
}

// TestHTTPConnectTunnel drives the HTTP CONNECT path: the byte stream after the
// 200 response must reach the echo server and come back.
func TestHTTPConnectTunnel(t *testing.T) {
	target := echoServer(t)
	px := startProxy(t)

	c, err := net.DialTimeout("tcp", px.Addr(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if want := "HTTP/1.1 200"; len(status) < len(want) || status[:len(want)] != want {
		t.Fatalf("CONNECT status = %q, want %q...", status, want)
	}
	// Consume the rest of the response headers (the blank line).
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
}

// TestSOCKS5Connect drives the SOCKS5 CONNECT path against the echo server.
func TestSOCKS5Connect(t *testing.T) {
	target := echoServer(t)
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	px := startProxy(t)

	c, err := net.DialTimeout("tcp", px.Addr(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Greeting: VER=5, 1 method, no-auth.
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatal(err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("method selection = %v, want [5 0]", resp)
	}

	// CONNECT request with IPv4 destination.
	req := []byte{0x05, 0x01, 0x00, 0x01}
	req = append(req, net.ParseIP(host).To4()...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10) // VER REP RSV ATYP=1 + 4 addr + 2 port
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("SOCKS5 reply status = %d, want 0 (success)", reply[1])
	}

	if _, err := c.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("echo = %q, want pong", buf)
	}
}

// TestSOCKS5UnsupportedCommand checks a BIND/UDP command is rejected, not hung.
func TestSOCKS5UnsupportedCommand(t *testing.T) {
	px := startProxy(t)
	c, err := net.DialTimeout("tcp", px.Addr(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	c.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(c, make([]byte, 2))
	// CMD=0x02 (BIND), IPv4 0.0.0.0:0.
	c.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x07 {
		t.Fatalf("reply status = %d, want 7 (command not supported)", reply[1])
	}
}

// TestImplementsAddr is a trivial guard that Addr returns the bound port.
func TestImplementsAddr(t *testing.T) {
	px := startProxy(t)
	if _, _, err := net.SplitHostPort(px.Addr()); err != nil {
		t.Fatalf("Addr() = %q is not host:port: %v", px.Addr(), err)
	}
}
