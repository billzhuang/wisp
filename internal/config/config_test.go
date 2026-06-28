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
		[]string{"-hostname", "myterm", "-state-dir", "/s"},
		env(map[string]string{"USER": "alice"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "myterm" {
		t.Fatalf("hostname = %q, want myterm", c.Hostname)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.ProxyAddr != "127.0.0.1:0" {
		t.Fatalf("default proxy-addr = %q, want 127.0.0.1:0", c.ProxyAddr)
	}
}

func TestResolveShell(t *testing.T) {
	// Explicit flag wins.
	c := &Config{Shell: "/bin/zsh"}
	if got := c.ResolveShell(env(map[string]string{"SHELL": "/bin/bash"})); got != "/bin/zsh" {
		t.Fatalf("shell = %q, want /bin/zsh (flag)", got)
	}
	// Else $SHELL.
	c = &Config{}
	if got := c.ResolveShell(env(map[string]string{"SHELL": "/bin/bash"})); got != "/bin/bash" {
		t.Fatalf("shell = %q, want /bin/bash ($SHELL)", got)
	}
	// Else /bin/sh.
	if got := c.ResolveShell(env(nil)); got != "/bin/sh" {
		t.Fatalf("shell = %q, want /bin/sh (fallback)", got)
	}
}

func TestAuthKeyFromEnv(t *testing.T) {
	c, err := Parse(
		[]string{},
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
		[]string{},
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
		[]string{"-tags", "tag:flag"},
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
	c := &Config{Hostname: "wisp", StateDir: "/s", ClientSecret: "tskey-client-x"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: -client-secret without -tags")
	}
	c.Tags = []string{"tag:ci"}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate with tags: %v", err)
	}
}

func TestValidateClientSecretTagsSkippedWhenNoTailnet(t *testing.T) {
	// -no-tailnet bypasses tsnet entirely, so the tag rule doesn't apply.
	c := &Config{NoTailnet: true, ClientSecret: "tskey-client-x"}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate (no-tailnet): %v", err)
	}
}

func TestValidateTSNetRequirements(t *testing.T) {
	// Without -no-tailnet, hostname + state-dir are mandatory.
	c := &Config{}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing tsnet hostname/state-dir")
	}
	// With -no-tailnet they are not.
	c = &Config{NoTailnet: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("no-tailnet mode should not require tsnet fields: %v", err)
	}
}

func TestParseHelp(t *testing.T) {
	_, err := Parse([]string{"-h"}, env(nil))
	if err != flag.ErrHelp {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}

func TestStateDirDefaultFromHome(t *testing.T) {
	c, err := Parse([]string{}, env(map[string]string{"HOME": "/home/bob", "USER": "bob"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != "/home/bob/.config/wisp/tsnet" {
		t.Fatalf("state dir = %q", c.StateDir)
	}
}
