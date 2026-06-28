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

func TestClientSecretAndTagsFromEnv(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "dev-box"},
		env(map[string]string{
			"USER":             "u",
			"TS_CLIENT_SECRET": "tskey-client-xyz",
			"TS_TAGS":          "tag:ci, tag:e2e ,",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientSecret != "tskey-client-xyz" {
		t.Fatalf("client secret = %q", c.ClientSecret)
	}
	// Whitespace trimmed and empty entries dropped.
	if len(c.Tags) != 2 || c.Tags[0] != "tag:ci" || c.Tags[1] != "tag:e2e" {
		t.Fatalf("tags = %#v, want [tag:ci tag:e2e]", c.Tags)
	}
}

func TestTagsFlagOverridesEnv(t *testing.T) {
	c, err := Parse(
		[]string{"-host", "h", "-tags", "tag:flag"},
		env(map[string]string{"USER": "u", "TS_TAGS": "tag:env"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tags) != 1 || c.Tags[0] != "tag:flag" {
		t.Fatalf("tags = %#v, want [tag:flag]", c.Tags)
	}
}

func TestValidateClientSecretRequiresTags(t *testing.T) {
	c := &Config{Host: "h", User: "u", Hostname: "wisp", StateDir: "/s", KnownHosts: "/kh", ClientSecret: "tskey-client-x"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: -client-secret without -tags")
	}
	c.Tags = []string{"tag:ci"}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate with tags: %v", err)
	}
}

func TestValidateClientSecretTagsSkippedWhenDirect(t *testing.T) {
	// -direct bypasses tsnet entirely, so the tag rule doesn't apply.
	c := &Config{Host: "h", User: "u", Direct: true, KnownHosts: "/kh", ClientSecret: "tskey-client-x"}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate (direct): %v", err)
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
