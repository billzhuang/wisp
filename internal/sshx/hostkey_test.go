package sshx

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// generateTestKey creates a fresh RSA public key for use in unit tests.
func generateTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer.PublicKey()
}

// dummyAddr implements net.Addr for unit tests that need to pass one to a
// HostKeyCallback without a real network connection.
type dummyAddr struct{ s string }

func (d dummyAddr) Network() string { return "tcp" }
func (d dummyAddr) String() string  { return d.s }

// TestKnownHostsEmptyPathReturnsError checks that an empty path is immediately
// rejected rather than falling through to a misleading file-system error.
func TestKnownHostsEmptyPathReturnsError(t *testing.T) {
	_, err := KnownHostsCallback("", true)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestKnownHostsCreatesFileIfAbsent verifies that the callback creates the
// known_hosts file (and any missing parent directories) when they do not exist.
func TestKnownHostsCreatesFileIfAbsent(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "sub", "known_hosts")

	_, err := KnownHostsCallback(khPath, false)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}
	if _, err := os.Stat(khPath); os.IsNotExist(err) {
		t.Fatal("known_hosts file was not created")
	}
}

// TestKnownHostsNoTOFURejectsUnknown checks that with trustOnFirstUse=false an
// unknown host is rejected (not silently accepted).
func TestKnownHostsNoTOFURejectsUnknown(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := KnownHostsCallback(khPath, false)
	if err != nil {
		t.Fatal(err)
	}

	pub := generateTestKey(t)
	addr := dummyAddr{"127.0.0.1:22"}
	// Use "host:port" format as the SSH client provides it.
	err = cb("unknown.example.com:22", addr, pub)
	if err == nil {
		t.Fatal("expected rejection of unknown host when TOFU is disabled")
	}
}

// TestKnownHostsTOFUAcceptsAndRecords verifies that an unknown host is accepted
// on first use and its key is written to the known_hosts file.
func TestKnownHostsTOFUAcceptsAndRecords(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}

	pub := generateTestKey(t)
	addr := dummyAddr{"127.0.0.1:22"}
	// Host:port as the SSH client would supply.
	if err := cb("newhost.example.com:22", addr, pub); err != nil {
		t.Fatalf("TOFU: unexpected rejection of unknown host: %v", err)
	}

	data, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("known_hosts not written after TOFU accept")
	}
}

// TestKnownHostsTOFURejectsMismatch ensures that a known host whose key has
// changed is always rejected — even with trustOnFirstUse=true.
func TestKnownHostsTOFURejectsMismatch(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")

	// First TOFU: accept key A and record it.
	cb, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}
	pub1 := generateTestKey(t)
	addr := dummyAddr{"127.0.0.1:22"}
	const hostname = "myhost.example.com:22"
	if err := cb(hostname, addr, pub1); err != nil {
		t.Fatalf("first TOFU failed: %v", err)
	}

	// Reload callback to pick up the freshly written entry.
	cb2, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}

	// Present a *different* key for the same host — must be rejected.
	pub2 := generateTestKey(t)
	err = cb2(hostname, addr, pub2)
	if err == nil {
		t.Fatal("expected rejection of changed host key (possible MITM)")
	}
}

// TestKnownHostsExistingKeyAccepted verifies that a key already recorded in
// known_hosts is accepted without TOFU.
func TestKnownHostsExistingKeyAccepted(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")

	// Seed the file via a TOFU callback.
	cb, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}
	pub := generateTestKey(t)
	addr := dummyAddr{"127.0.0.1:22"}
	const hostname = "goodhost.example.com:22"
	if err := cb(hostname, addr, pub); err != nil {
		t.Fatalf("TOFU seed: %v", err)
	}

	// Now reload *without* TOFU; the recorded key must be accepted.
	cb2, err := KnownHostsCallback(khPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb2(hostname, addr, pub); err != nil {
		t.Fatalf("known key rejected: %v", err)
	}
}

// TestInsecureIgnoreHostKey verifies the callback accepts any key without error.
func TestInsecureIgnoreHostKey(t *testing.T) {
	cb := InsecureIgnoreHostKey()
	if cb == nil {
		t.Fatal("InsecureIgnoreHostKey returned nil")
	}

	pub := generateTestKey(t)
	addr := dummyAddr{"1.2.3.4:22"}
	var remote net.Addr = addr
	if err := cb("any-host:22", remote, pub); err != nil {
		t.Fatalf("InsecureIgnoreHostKey rejected a key: %v", err)
	}
}

// TestKnownHostsConcurrentTOFU checks that two goroutines can simultaneously
// trigger TOFU for two *different* hosts without corrupting the file.
func TestKnownHostsConcurrentTOFU(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}

	pub1 := generateTestKey(t)
	pub2 := generateTestKey(t)
	addr := dummyAddr{"127.0.0.1:22"}

	errs := make(chan error, 2)
	go func() { errs <- cb("host1.example.com:22", addr, pub1) }()
	go func() { errs <- cb("host2.example.com:22", addr, pub2) }()

	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent TOFU error: %v", err)
		}
	}

	data, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("known_hosts empty after concurrent TOFU")
	}
}