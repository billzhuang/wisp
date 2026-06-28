// Package config turns command-line flags and environment variables into a
// validated runtime configuration for wisp. Keeping it separate from main keeps
// the wiring testable.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	// Shell is the program launched in the terminal. Empty resolves to $SHELL,
	// then /bin/sh (see ResolveShell).
	Shell string
	// Command, if non-empty, runs `shell -c <Command>` instead of an interactive
	// login shell. Handy for one-shot use and for tests.
	Command string

	// ProxyAddr is the bind address for the embedded tailnet egress proxy.
	ProxyAddr string

	// Hostname is the tsnet node name shown in the tailnet.
	Hostname string
	// StateDir persists the embedded node identity (no daemon/app).
	StateDir string
	// AuthKey pre-authenticates the node; empty means interactive login URL.
	AuthKey string
	// ClientSecret is a Tailscale OAuth client secret (tskey-client-…). When set,
	// the node mints its own short-lived auth key at startup instead of using a
	// long-lived AuthKey — the modern, revocable way to authenticate headless
	// nodes (CI, servers). OAuth-minted nodes must be tagged, so Tags is
	// required alongside it.
	ClientSecret string
	// Tags are the ACL tags advertised by the node (e.g. "tag:ci"). Required
	// when authenticating via ClientSecret; optional with a tagged AuthKey.
	Tags []string
	// ControlURL overrides the coordination server (Headscale, self-hosted).
	ControlURL string
	// Ephemeral registers the node as ephemeral.
	Ephemeral bool

	// NoTailnet skips the embedded tsnet node entirely: the proxy then dials via
	// the OS network stack. For a plain local terminal and for hermetic tests;
	// tailnet-only resources are unreachable in this mode.
	NoTailnet bool

	// ShowVersion prints the version and exits.
	ShowVersion bool
	// DoUpdate checks for and installs a newer release, then exits.
	DoUpdate bool
	// NoUpdateCheck disables the best-effort update notice on startup.
	NoUpdateCheck bool
}

// ResolveShell returns the shell to launch: the configured value, else $SHELL,
// else /bin/sh.
func (c *Config) ResolveShell(getenv func(string) string) string {
	if c.Shell != "" {
		return c.Shell
	}
	if sh := getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// Validate checks required fields and applies cross-field rules.
func (c *Config) Validate() error {
	if !c.NoTailnet {
		if c.Hostname == "" {
			return errors.New("config: -hostname (tsnet node name) is required unless -no-tailnet")
		}
		if c.StateDir == "" {
			return errors.New("config: -state-dir is required unless -no-tailnet (node identity must persist)")
		}
		// OAuth-minted nodes must be tagged: the control plane assigns identity
		// from the tag, so a client secret without tags can't register.
		if c.ClientSecret != "" && len(c.Tags) == 0 {
			return errors.New("config: -tags is required with -client-secret (OAuth-authenticated nodes must be tagged)")
		}
	}
	return nil
}

// Parse builds a Config from the given argument list (os.Args[1:]) and the
// environment. It returns flag.ErrHelp when -h/-help was requested.
func Parse(args []string, getenv func(string) string) (*Config, error) {
	fs := flag.NewFlagSet("wisp", flag.ContinueOnError)
	c := &Config{}

	defStateDir := ""
	if home := getenv("HOME"); home != "" {
		defStateDir = filepath.Join(home, ".config", "wisp", "tsnet")
	}

	fs.StringVar(&c.Shell, "shell", "", "shell to launch in the terminal (default $SHELL, else /bin/sh)")
	fs.StringVar(&c.Command, "command", "", "run a single command instead of an interactive shell")
	fs.StringVar(&c.ProxyAddr, "proxy-addr", "127.0.0.1:0", "bind address for the embedded tailnet egress proxy")

	fs.StringVar(&c.Hostname, "hostname", "wisp", "tsnet node name advertised on the tailnet")
	fs.StringVar(&c.StateDir, "state-dir", defStateDir, "directory persisting the embedded tsnet node identity")
	// Secret-bearing flags default to empty, not getenv(...): flag.PrintDefaults
	// echoes a non-empty default into `wisp -h`, which would leak the key/secret.
	// Their env fallback is applied after Parse instead (see below).
	fs.StringVar(&c.AuthKey, "authkey", "", "tsnet auth key (else interactive login URL; env TS_AUTHKEY)")
	fs.StringVar(&c.ClientSecret, "client-secret", "", "Tailscale OAuth client secret (tskey-client-…); mints a short-lived key, requires -tags (env TS_CLIENT_SECRET)")
	var tags string
	fs.StringVar(&tags, "tags", getenv("TS_TAGS"), "comma-separated ACL tags to advertise (e.g. tag:ci); required with -client-secret")
	fs.StringVar(&c.ControlURL, "control-url", getenv("TS_CONTROL_URL"), "coordination server URL (Headscale/self-hosted)")
	fs.BoolVar(&c.Ephemeral, "ephemeral", false, "register the node as ephemeral")

	fs.BoolVar(&c.NoTailnet, "no-tailnet", false, "skip the embedded Tailscale node; proxy via the OS network stack (local-only/testing)")

	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	fs.BoolVar(&c.DoUpdate, "update", false, "check for and install a newer release, then exit")
	fs.BoolVar(&c.NoUpdateCheck, "no-update-check", false, "do not check for updates on startup")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// Env fallback for the secret-bearing flags, applied here (not as a flag
	// default) so the secret never appears in `wisp -h`. An explicit flag wins.
	if c.AuthKey == "" {
		c.AuthKey = getenv("TS_AUTHKEY")
	}
	if c.ClientSecret == "" {
		c.ClientSecret = getenv("TS_CLIENT_SECRET")
	}
	c.Tags = splitTags(tags)
	return c, nil
}

// splitTags turns a comma-separated tag list into a trimmed, empty-free slice.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// MustHome returns the user home dir or panics; used to print defaults in help.
func MustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("config: cannot determine home dir: %v", err))
	}
	return h
}
