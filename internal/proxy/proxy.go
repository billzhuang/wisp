// Package proxy runs a small local proxy that bridges loopback TCP connections
// onto the embedded Tailscale node. Programs launched in wisp's terminal reach
// tailnet (and subnet-router) resources by pointing their HTTP(S)_PROXY /
// ALL_PROXY at this listener — no system TUN device, no DNS changes, no
// Tailscale app installed.
//
// This is the bridge the architecture needs: tsnet is a userspace network stack
// inside our process, so it cannot transparently capture other programs'
// traffic the way the system Tailscale client's TUN device does. Instead, every
// connection this proxy accepts is fulfilled by dialer.Dial — i.e. it rides the
// embedded tsnet node — and the hostname travels with the request, so MagicDNS
// names resolve through the tailnet without touching system DNS.
//
// One listener speaks both protocols, distinguished by the first byte the
// client sends: 0x05 is the SOCKS5 version byte; anything else starts an HTTP
// request line. So a single address works as both http://host:port and
// socks5h://host:port.
package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/billzhuang/wisp/internal/transport"
)

// dialTimeout bounds how long a single proxied CONNECT waits for the tailnet
// dial to establish.
const dialTimeout = 30 * time.Second

// Server is a running proxy listener.
type Server struct {
	ln     net.Listener
	dialer transport.Dialer
}

// Start binds to addr (e.g. "127.0.0.1:0") and serves until Close. Every
// connection it proxies is dialed through dialer, so all proxied traffic rides
// whatever transport the dialer represents (tsnet in production, the OS stack
// under -no-tailnet and in tests).
func Start(addr string, dialer transport.Dialer) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, dialer: dialer}
	go s.serve()
	return s, nil
}

// Addr is the resolved listen address, e.g. "127.0.0.1:54321".
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops accepting new connections.
func (s *Server) Close() error { return s.ln.Close() }

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		s.handleSOCKS5(c, br)
		return
	}
	s.handleHTTP(c, br)
}

// dial opens a connection to addr ("host:port") through the configured dialer.
func (s *Server) dial(addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	return s.dialer.Dial(ctx, "tcp", addr)
}

// handleHTTP serves an HTTP proxy request: CONNECT (tunnel for HTTPS and any
// other TLS/TCP protocol) or a plain absolute-URI request forwarded to origin.
func (s *Server) handleHTTP(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		remote, err := s.dial(req.Host) // CONNECT target is already host:port
		if err != nil {
			io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		defer remote.Close()
		io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		relay(br, c, remote)
		return
	}

	// Plain HTTP proxying: forward one request to the origin and stream the
	// response back. host may omit the port.
	if req.URL.Host == "" {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	addr := req.URL.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "80")
	}
	remote, err := s.dial(addr)
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer remote.Close()

	// Rewrite to origin form (drop scheme+host so req.Write emits just the path)
	// and strip hop-by-hop proxy headers before forwarding.
	req.URL.Scheme = ""
	req.URL.Host = ""
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	// Forward a single request/response and ask the origin to close afterwards:
	// req.Write emits "Connection: close", so the origin ends the stream once the
	// response is sent and the io.Copy below terminates instead of blocking
	// forever on a keep-alive connection. (HTTPS and any reused connection go
	// through CONNECT above, which has no such limit.)
	req.Close = true
	if err := req.Write(remote); err != nil {
		return
	}
	io.Copy(c, remote)
}

// handleSOCKS5 serves a no-auth SOCKS5 CONNECT request (RFC 1928). socks5h
// clients send a domain name, which we resolve through the dialer (tsnet's
// MagicDNS) rather than locally.
func (s *Server) handleSOCKS5(c net.Conn, br *bufio.Reader) {
	// Greeting: VER, NMETHODS, METHODS...
	ver, err := br.ReadByte()
	if err != nil || ver != 0x05 {
		return
	}
	nmethods, err := br.ReadByte()
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, br, int64(nmethods)); err != nil {
		return
	}
	// Select "no authentication required".
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	cmd, atyp := hdr[1], hdr[3]

	host, err := readSOCKSAddr(br, atyp)
	if err != nil {
		socksReply(c, 0x08) // address type not supported
		return
	}
	var portb [2]byte
	if _, err := io.ReadFull(br, portb[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portb[:])

	if cmd != 0x01 { // only CONNECT is supported
		socksReply(c, 0x07) // command not supported
		return
	}

	remote, err := s.dial(net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		socksReply(c, 0x05) // connection refused
		return
	}
	defer remote.Close()

	socksReply(c, 0x00) // succeeded
	relay(br, c, remote)
}

// readSOCKSAddr reads a SOCKS5 destination address of the given ATYP.
func readSOCKSAddr(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03: // domain name
		l, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, int(l))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return string(b), nil
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	default:
		return "", fmt.Errorf("proxy: unsupported SOCKS5 address type %d", atyp)
	}
}

// socksReply writes a SOCKS5 reply with the given status and a zero BND.ADDR.
func socksReply(c net.Conn, status byte) {
	c.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

// relay copies bytes both ways between the client (reading via clientR, which
// wraps any bytes already buffered past the handshake) and the remote, closing
// each direction's write side when its source is exhausted. It returns once
// both directions are done.
func relay(clientR io.Reader, client, remote net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(remote, clientR)
		halfCloseWrite(remote)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, remote)
		halfCloseWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// halfCloseWrite shuts down the write half of c when supported (TCP), so the
// peer sees EOF without tearing down the read half mid-transfer.
func halfCloseWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
