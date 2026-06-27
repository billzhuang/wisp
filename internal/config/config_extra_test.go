// Additional tests for config.go covering flags and edge cases not exercised by
// the baseline config_test.go.
package config

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Addr edge cases
// ---------------------------------------------------------------------------

func TestAddrBareHostAppends22(t *testing.T) {
	c := &Config{Host: "myhost"}
	if got := c.Addr(); got != "myhost:22" {
		t.Errorf("Addr() = %q, want myhost:22", got)
	}
}

// A host value that contains a colon (e.g. an IPv6 literal or explicit port)
// must be returned as-is.
func TestAddrHostWithColonIsUnchanged(t *testing.T) {
	cases := []string{
		"host:8022",
		"[::1]:22",
		"192.168.1.1:2222",
	}
	for _, h := range cases {
		c := &Config{Host: h}
		if got := c.Addr(); got != h {
			t.Errorf("Addr() for %q = %q, want unchanged", h, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Validate — user field
// ---------------------------------------------------------------------------

func TestValidateRequiresUser(t *testing.T) {
	c := &Config{Host: "h", Direct: true, InsecureHostKey: true}
	// User is empty — must fail.
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing -user")
	}
}

func TestValidateRequiresUserErrorMessage(t *testing.T) {
	c := &Config{Host: "h", Direct: true, InsecureHostKey: true}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "user") {
		t.Errorf("error = %q, expected mention of 'user'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Validate — tsnet fields
// ---------------------------------------------------------------------------

func TestValidateTSNetMissingHostname(t *testing.T) {
	// Direct=false but Hostname empty (StateDir present).
	c := &Config{Host: "h", User: "u", StateDir: "/s", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet hostname")
	}
}

func TestValidateTSNetMissingStateDir(t *testing.T) {
	// Direct=false but StateDir empty (Hostname present).
	c := &Config{Host: "h", User: "u", Hostname: "n", KnownHosts: "/kh"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet state-dir")
	}
}

func TestValidateDirectModeBypasesHostnameStateDir(t *testing.T) {
	// -direct: hostname and state-dir must not be required.
	c := &Config{Host: "h", User: "u", Direct: true, InsecureHostKey: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("direct mode should not need tsnet fields: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Parse — boolean action flags
// ---------------------------------------------------------------------------

func TestParseVersionFlag(t *testing.T) {
	c, err := Parse([]string{"-version"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.ShowVersion {
		t.Error("ShowVersion should be true after -version flag")
	}
}

func TestParseUpdateFlag(t *testing.T) {
	c, err := Parse([]string{"-update"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.DoUpdate {
		t.Error("DoUpdate should be true after -update flag")
	}
}

func TestParseNoUpdateCheckFlag(t *testing.T) {
	c, err := Parse([]string{"-no-update-check"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.NoUpdateCheck {
		t.Error("NoUpdateCheck should be true after -no-update-check flag")
	}
}

func TestParseEphemeralFlag(t *testing.T) {
	c, err := Parse([]string{"-ephemeral"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ephemeral {
		t.Error("Ephemeral should be true after -ephemeral flag")
	}
}

func TestParseDirectFlag(t *testing.T) {
	c, err := Parse([]string{"-direct"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Direct {
		t.Error("Direct should be true after -direct flag")
	}
}

func TestParseInsecureHostKeyFlag(t *testing.T) {
	c, err := Parse([]string{"-insecure-host-key"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.InsecureHostKey {
		t.Error("InsecureHostKey should be true after -insecure-host-key flag")
	}
}

// ---------------------------------------------------------------------------
// Parse — string flags
// ---------------------------------------------------------------------------

func TestParseCommandFlag(t *testing.T) {
	c, err := Parse([]string{"-command", "ls -la"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Command != "ls -la" {
		t.Errorf("Command = %q, want 'ls -la'", c.Command)
	}
}

func TestParseIdentityFileFlag(t *testing.T) {
	c, err := Parse([]string{"-i", "/home/user/.ssh/id_ed25519"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.IdentityFile != "/home/user/.ssh/id_ed25519" {
		t.Errorf("IdentityFile = %q", c.IdentityFile)
	}
}

func TestParseHostnameFlag(t *testing.T) {
	c, err := Parse([]string{"-hostname", "mynode"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "mynode" {
		t.Errorf("Hostname = %q, want mynode", c.Hostname)
	}
}

func TestParseHostnameDefault(t *testing.T) {
	// No -hostname flag → default should be "wisp".
	c, err := Parse([]string{}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "wisp" {
		t.Errorf("default Hostname = %q, want wisp", c.Hostname)
	}
}

func TestParseKnownHostsFlag(t *testing.T) {
	c, err := Parse([]string{"-known-hosts", "/etc/wisp/known_hosts"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.KnownHosts != "/etc/wisp/known_hosts" {
		t.Errorf("KnownHosts = %q", c.KnownHosts)
	}
}

func TestParseStateDirFlag(t *testing.T) {
	c, err := Parse([]string{"-state-dir", "/var/lib/wisp"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "/var/lib/wisp" {
		t.Errorf("StateDir = %q, want /var/lib/wisp", c.StateDir)
	}
}

// ---------------------------------------------------------------------------
// Parse — no HOME env var
// ---------------------------------------------------------------------------

func TestStateDirEmptyWhenNoHome(t *testing.T) {
	// When HOME is absent, the computed defaults should be empty strings.
	c, err := Parse([]string{}, env(map[string]string{"HOME": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "" {
		t.Errorf("StateDir = %q, want empty when HOME is unset", c.StateDir)
	}
	if c.KnownHosts != "" {
		t.Errorf("KnownHosts = %q, want empty when HOME is unset", c.KnownHosts)
	}
}

// ---------------------------------------------------------------------------
// Parse — unknown flag
// ---------------------------------------------------------------------------

func TestParseUnknownFlagReturnsError(t *testing.T) {
	_, err := Parse([]string{"-not-a-real-flag"}, env(nil))
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}

// ---------------------------------------------------------------------------
// Config field defaults
// ---------------------------------------------------------------------------

func TestParseUserFromFlag(t *testing.T) {
	// -user flag overrides USER env.
	c, err := Parse([]string{"-user", "bob"}, env(map[string]string{"USER": "alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "bob" {
		t.Errorf("User = %q, want bob", c.User)
	}
}

func TestParseControlURLFromFlag(t *testing.T) {
	c, err := Parse([]string{"-control-url", "https://headscale.example.com"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ControlURL != "https://headscale.example.com" {
		t.Errorf("ControlURL = %q", c.ControlURL)
	}
}

func TestParseHostFlag(t *testing.T) {
	c, err := Parse([]string{"-host", "dev.example"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "dev.example" {
		t.Errorf("Host = %q, want dev.example", c.Host)
	}
}

// ---------------------------------------------------------------------------
// Validate — successful path
// ---------------------------------------------------------------------------

func TestValidateSuccessWithAllRequiredFields(t *testing.T) {
	c := &Config{
		Host:       "myhost",
		User:       "alice",
		Hostname:   "wisp-node",
		StateDir:   "/tmp/state",
		KnownHosts: "/tmp/known_hosts",
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() failed for valid config: %v", err)
	}
}

// Validate that all individual required-field errors mention the flag name.
func TestValidateErrorsMentionFlagNames(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		keyword string
	}{
		{"missing host", Config{User: "u", Direct: true, InsecureHostKey: true}, "host"},
		{"missing user", Config{Host: "h", Direct: true, InsecureHostKey: true}, "user"},
		{"missing hostname", Config{Host: "h", User: "u", StateDir: "/s", KnownHosts: "/k"}, "hostname"},
		{"missing state-dir", Config{Host: "h", User: "u", Hostname: "n", KnownHosts: "/k"}, "state-dir"},
		{"missing known-hosts", Config{Host: "h", User: "u", Direct: true}, "known-hosts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.keyword) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.keyword)
			}
		})
	}
}
