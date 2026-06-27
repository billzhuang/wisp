package config

import (
	"flag"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseDefaults(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "dev-box", "-hostname", "myterm", "-state-dir", "/s", "-known-hosts", "/kh"},
		env(map[string]string{"USER": "alice"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "alice" {
		t.Fatalf("user = %q, want alice (from USER env)", c.User)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.Addr() != "dev-box:22" {
		t.Fatalf("addr = %q, want dev-box:22", c.Addr())
	}
}

func TestAddrKeepsExplicitPort(t *testing.T) {
	c := &Config{Host: "dev-box:2222"}
	if c.Addr() != "dev-box:2222" {
		t.Fatalf("addr = %q", c.Addr())
	}
}

func TestAuthKeyFromEnv(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "h"},
		env(map[string]string{"USER": "u", "TS_AUTHKEY": "tskey-abc", "TS_CONTROL_URL": "https://hs.example"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthKey != "tskey-abc" {
		t.Fatalf("authkey = %q", c.AuthKey)
	}
	if c.ControlURL != "https://hs.example" {
		t.Fatalf("control url = %q", c.ControlURL)
	}
}

func TestValidateRequiresHost(t *testing.T) {
	c := &Config{User: "u", Hostname: "h", StateDir: "/s", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestValidateTSNetRequirements(t *testing.T) {
	// Without -direct, hostname + state-dir are mandatory.
	c := &Config{Host: "h", User: "u", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet hostname/state-dir")
	}
	// With -direct they are not.
	c = &Config{Host: "h", User: "u", KnownHosts: "/kh", Direct: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("direct mode should not require tsnet fields: %v", err)
	}
}

func TestValidateHostKeyRequirement(t *testing.T) {
	c := &Config{Host: "h", User: "u", Direct: true}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: known-hosts required unless insecure")
	}
	c.InsecureHostKey = true
	if err := c.Validate(); err != nil {
		t.Fatalf("insecure host key should satisfy requirement: %v", err)
	}
}

func TestParseHelp(t *testing.T) {
	_, err := Parse([]string{"-h"}, env(nil))
	if err != flag.ErrHelp {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}

func TestStateDirDefaultFromHome(t *testing.T) {
	c, err := Parse([]string{"-host", "h"}, env(map[string]string{"HOME": "/home/bob", "USER": "bob"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "/home/bob/.config/wisp/tsnet" {
		t.Fatalf("state dir = %q", c.StateDir)
	}
	if c.KnownHosts != "/home/bob/.config/wisp/known_hosts" {
		t.Fatalf("known hosts = %q", c.KnownHosts)
	}
}

// ---------------------------------------------------------------------------
// Additional edge-case tests
// ---------------------------------------------------------------------------

// TestAddrWithIPv6 verifies that a host containing ":" (IPv6 or explicit port)
// is returned unchanged by Addr().
func TestAddrWithIPv6(t *testing.T) {
	// IPv6 address with port — contains ":".
	c := &Config{Host: "[::1]:2222"}
	if got := c.Addr(); got != "[::1]:2222" {
		t.Fatalf("Addr() = %q, want [::1]:2222", got)
	}
}

// TestAddrNoHomeNoDefaults verifies that with no HOME environment variable
// the default state-dir and known-hosts are empty strings.
func TestAddrNoHomeNoDefaults(t *testing.T) {
	c, err := Parse([]string{"-host", "h"}, env(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "" {
		t.Fatalf("state dir = %q, want empty when HOME unset", c.StateDir)
	}
	if c.KnownHosts != "" {
		t.Fatalf("known hosts = %q, want empty when HOME unset", c.KnownHosts)
	}
}

// TestValidateRequiresUser checks that Validate rejects an empty User.
func TestValidateRequiresUser(t *testing.T) {
	c := &Config{Host: "h", Hostname: "n", StateDir: "/s", KnownHosts: "/kh"}
	// User is deliberately left empty.
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing user")
	}
}

// TestValidateTSNetMissingHostname checks the specific error for missing hostname.
func TestValidateTSNetMissingHostname(t *testing.T) {
	c := &Config{Host: "h", User: "u", StateDir: "/s", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: hostname required for tsnet")
	}
}

// TestValidateTSNetMissingStateDir checks the specific error for missing state-dir.
func TestValidateTSNetMissingStateDir(t *testing.T) {
	c := &Config{Host: "h", User: "u", Hostname: "n", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: state-dir required for tsnet")
	}
}

// TestParseBoolFlags checks that boolean flags parse correctly.
func TestParseBoolFlags(t *testing.T) {
	c, err := Parse(
		[]string{
			"-host", "h",
			"-ephemeral",
			"-direct",
			"-insecure-host-key",
			"-version",
			"-update",
			"-no-update-check",
		},
		env(map[string]string{"USER": "u"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ephemeral {
		t.Error("Ephemeral should be true")
	}
	if !c.Direct {
		t.Error("Direct should be true")
	}
	if !c.InsecureHostKey {
		t.Error("InsecureHostKey should be true")
	}
	if !c.ShowVersion {
		t.Error("ShowVersion should be true")
	}
	if !c.DoUpdate {
		t.Error("DoUpdate should be true")
	}
	if !c.NoUpdateCheck {
		t.Error("NoUpdateCheck should be true")
	}
}

// TestParseCommandFlag checks that the -command flag is captured.
func TestParseCommandFlag(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "h", "-command", "ls -la"},
		env(map[string]string{"USER": "u"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Command != "ls -la" {
		t.Fatalf("Command = %q, want 'ls -la'", c.Command)
	}
}

// TestParseIdentityFile checks the -i flag.
func TestParseIdentityFile(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "h", "-i", "/home/user/.ssh/id_rsa"},
		env(map[string]string{"USER": "u"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.IdentityFile != "/home/user/.ssh/id_rsa" {
		t.Fatalf("IdentityFile = %q", c.IdentityFile)
	}
}

// TestParseFlagOverridesEnv verifies that explicit flags take precedence over
// environment variables for the same setting.
func TestParseFlagOverridesEnv(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "h", "-user", "explicit-user"},
		env(map[string]string{"USER": "env-user"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "explicit-user" {
		t.Fatalf("user = %q, want explicit-user (flag should override env)", c.User)
	}
}

// TestParseHostnameDefault checks that the tsnet hostname defaults to "wisp".
func TestParseHostnameDefault(t *testing.T) {
	c, err := Parse([]string{"-host", "h"}, env(map[string]string{"USER": "u"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "wisp" {
		t.Fatalf("hostname = %q, want 'wisp'", c.Hostname)
	}
}

// TestParseHostExplicitPort ensures parsing a host:port arg is preserved.
func TestParseHostExplicitPort(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "myhost:2222"},
		env(map[string]string{"USER": "u"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "myhost:2222" {
		t.Fatalf("Host = %q", c.Host)
	}
	if c.Addr() != "myhost:2222" {
		t.Fatalf("Addr() = %q, want myhost:2222 (no extra port)", c.Addr())
	}
}

// TestValidateAllFieldsValidDirect is a positive test for the minimum valid
// direct-mode config.
func TestValidateAllFieldsValidDirect(t *testing.T) {
	c := &Config{
		Host:            "myhost",
		User:            "alice",
		Direct:          true,
		InsecureHostKey: true,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid direct config rejected: %v", err)
	}
}

// TestValidateAllFieldsValidTSNet is a positive test for a valid tsnet config.
func TestValidateAllFieldsValidTSNet(t *testing.T) {
	c := &Config{
		Host:       "myhost",
		User:       "alice",
		Hostname:   "wisp-node",
		StateDir:   "/state",
		KnownHosts: "/kh",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid tsnet config rejected: %v", err)
	}
}
