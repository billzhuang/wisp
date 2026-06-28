// Additional tests for config.go covering flags and edge cases not exercised by
// the baseline config_test.go.
package config

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Validate — tsnet fields
// ---------------------------------------------------------------------------

func TestValidateTSNetMissingHostname(t *testing.T) {
	// NoTailnet=false but Hostname empty (StateDir present).
	c := &Config{StateDir: "/s"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet hostname")
	}
}

func TestValidateTSNetMissingStateDir(t *testing.T) {
	// NoTailnet=false but StateDir empty (Hostname present).
	c := &Config{Hostname: "n"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet state-dir")
	}
}

func TestValidateNoTailnetBypassesHostnameStateDir(t *testing.T) {
	// -no-tailnet: hostname and state-dir must not be required.
	c := &Config{NoTailnet: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("no-tailnet mode should not need tsnet fields: %v", err)
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

func TestParseNoTailnetFlag(t *testing.T) {
	c, err := Parse([]string{"-no-tailnet"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.NoTailnet {
		t.Error("NoTailnet should be true after -no-tailnet flag")
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

func TestParseShellFlag(t *testing.T) {
	c, err := Parse([]string{"-shell", "/bin/zsh"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Shell != "/bin/zsh" {
		t.Errorf("Shell = %q, want /bin/zsh", c.Shell)
	}
}

func TestParseProxyAddrFlag(t *testing.T) {
	c, err := Parse([]string{"-proxy-addr", "127.0.0.1:9999"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ProxyAddr != "127.0.0.1:9999" {
		t.Errorf("ProxyAddr = %q, want 127.0.0.1:9999", c.ProxyAddr)
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
	// When HOME is absent, the computed default should be an empty string.
	c, err := Parse([]string{}, env(map[string]string{"HOME": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "" {
		t.Errorf("StateDir = %q, want empty when HOME is unset", c.StateDir)
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
// Parse — tsnet flags
// ---------------------------------------------------------------------------

func TestParseControlURLFromFlag(t *testing.T) {
	c, err := Parse([]string{"-control-url", "https://headscale.example.com"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ControlURL != "https://headscale.example.com" {
		t.Errorf("ControlURL = %q", c.ControlURL)
	}
}

// ---------------------------------------------------------------------------
// Validate — successful path
// ---------------------------------------------------------------------------

func TestValidateSuccessWithAllRequiredFields(t *testing.T) {
	c := &Config{
		Hostname: "wisp-node",
		StateDir: "/tmp/state",
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() failed for valid config: %v", err)
	}
}

// Validate that the tsnet required-field errors mention the flag name.
func TestValidateErrorsMentionFlagNames(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		keyword string
	}{
		{"missing hostname", Config{StateDir: "/s"}, "hostname"},
		{"missing state-dir", Config{Hostname: "n"}, "state-dir"},
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
