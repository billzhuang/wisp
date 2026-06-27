package sshx

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// fakeAddr satisfies net.Addr for host-key callback invocations.
type fakeAddr struct{ s string }

func (f fakeAddr) Network() string { return "tcp" }
func (f fakeAddr) String() string  { return f.s }

// genPublicKey generates a random RSA public key for testing.
func genPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	return pub
}

// writeKnownHostEntry writes a single known_hosts entry for hostname/key.
func writeKnownHostEntry(t *testing.T, path, hostname string, key ssh.PublicKey) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open known_hosts for write: %v", err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write known_hosts entry: %v", err)
	}
}

// ---------------------------------------------------------------------------
// KnownHostsCallback — empty path
// ---------------------------------------------------------------------------

func TestKnownHostsCallbackEmptyPath(t *testing.T) {
	_, err := KnownHostsCallback("", true)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q does not mention 'empty'", err.Error())
	}
}

func TestKnownHostsCallbackEmptyPathNoTOFU(t *testing.T) {
	_, err := KnownHostsCallback("", false)
	if err == nil {
		t.Fatal("expected error for empty path (non-TOFU), got nil")
	}
}

// ---------------------------------------------------------------------------
// KnownHostsCallback — file creation
// ---------------------------------------------------------------------------

func TestKnownHostsCallbackCreatesFileMkdirAll(t *testing.T) {
	// Supply a path whose parent directory does not yet exist.
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "known_hosts")

	_, err := KnownHostsCallback(path, false)
	if err != nil {
		t.Fatalf("KnownHostsCallback should create missing dirs: %v", err)
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		t.Error("known_hosts file was not created")
	}
}

func TestKnownHostsCallbackUsesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	// Pre-create the file to confirm it is not overwritten.
	if err := os.WriteFile(path, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := KnownHostsCallback(path, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# existing") {
		t.Error("existing known_hosts content was overwritten")
	}
}

// ---------------------------------------------------------------------------
// KnownHostsCallback — strict mode (trustOnFirstUse=false)
// ---------------------------------------------------------------------------

func TestKnownHostsCallbackStrictRejectsUnknownHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	cb, err := KnownHostsCallback(path, false)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}

	key := genPublicKey(t)
	addr := fakeAddr{"127.0.0.1:22"}
	err = cb("127.0.0.1:22", addr, key)
	if err == nil {
		t.Fatal("strict mode should reject unknown host, got nil error")
	}
}

func TestKnownHostsCallbackStrictAcceptsKnownHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := genPublicKey(t)

	// Pre-record the key.
	writeKnownHostEntry(t, path, "127.0.0.1:22", key)

	cb, err := KnownHostsCallback(path, false)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}

	addr := fakeAddr{"127.0.0.1:22"}
	if err := cb("127.0.0.1:22", addr, key); err != nil {
		t.Fatalf("strict mode should accept recorded key, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// KnownHostsCallback — TOFU mode (trustOnFirstUse=true)
// ---------------------------------------------------------------------------

func TestKnownHostsCallbackTOFUAcceptsAndRecordsUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := genPublicKey(t)

	cb, err := KnownHostsCallback(path, true)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}

	addr := fakeAddr{"127.0.0.1:22"}
	if err := cb("127.0.0.1:22", addr, key); err != nil {
		t.Fatalf("TOFU should accept unknown host on first use, got: %v", err)
	}

	// The key must now be present in the file.
	data, _ := os.ReadFile(path)
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Error("known_hosts file was not written after TOFU acceptance")
	}
}

func TestKnownHostsCallbackTOFURejectsMismatchedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	// Record key1 for the host.
	key1 := genPublicKey(t)
	writeKnownHostEntry(t, path, "127.0.0.1:22", key1)

	cb, err := KnownHostsCallback(path, true)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}

	// Present key2 — the key has changed, TOFU must reject it.
	key2 := genPublicKey(t)
	addr := fakeAddr{"127.0.0.1:22"}
	err = cb("127.0.0.1:22", addr, key2)
	if err == nil {
		t.Fatal("TOFU must reject a changed host key (possible MITM)")
	}
	if !strings.Contains(err.Error(), "mismatch") && !strings.Contains(err.Error(), "MITM") {
		t.Logf("error = %q (expected MITM/mismatch mention)", err.Error())
	}
}

func TestKnownHostsCallbackTOFUAcceptsRecordedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := genPublicKey(t)

	// First call records the key.
	cb, err := KnownHostsCallback(path, true)
	if err != nil {
		t.Fatalf("KnownHostsCallback: %v", err)
	}
	addr := fakeAddr{"127.0.0.1:22"}
	if err := cb("127.0.0.1:22", addr, key); err != nil {
		t.Fatalf("first TOFU call failed: %v", err)
	}

	// Create a fresh callback from the same file — it should accept the now-recorded key.
	cb2, err := KnownHostsCallback(path, true)
	if err != nil {
		t.Fatalf("KnownHostsCallback (2nd): %v", err)
	}
	if err := cb2("127.0.0.1:22", addr, key); err != nil {
		t.Fatalf("second call with recorded key failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// InsecureIgnoreHostKey
// ---------------------------------------------------------------------------

func TestInsecureIgnoreHostKeyNotNil(t *testing.T) {
	cb := InsecureIgnoreHostKey()
	if cb == nil {
		t.Fatal("InsecureIgnoreHostKey() returned nil")
	}
}

func TestInsecureIgnoreHostKeyAcceptsAnyKey(t *testing.T) {
	cb := InsecureIgnoreHostKey()
	key := genPublicKey(t)
	addr := fakeAddr{"10.0.0.1:22"}
	if err := cb("any-host", addr, key); err != nil {
		t.Fatalf("InsecureIgnoreHostKey should accept any key, got: %v", err)
	}
}

// Boundary: InsecureIgnoreHostKey works with arbitrary hostnames (including ones
// that would normally be rejected by strict host-key verification).
func TestInsecureIgnoreHostKeyArbitraryHostname(t *testing.T) {
	cb := InsecureIgnoreHostKey()
	key := genPublicKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 2222}
	if err := cb("some-weird-hostname.example.com:2222", addr, key); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}