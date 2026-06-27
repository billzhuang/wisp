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
	// Host is the destination "host" or "host:port" on the tailnet. A bare host
	// defaults to port 22.
	Host string
	// User is the remote login name.
	User string
	// Command, if non-empty, runs a single command instead of a login shell.
	Command string

	// Hostname is the tsnet node name shown in the tailnet.
	Hostname string
	// StateDir persists the embedded node identity (no daemon/app).
	StateDir string
	// AuthKey pre-authenticates the node; empty means interactive login URL.
	AuthKey string
	// ControlURL overrides the coordination server (Headscale, self-hosted).
	ControlURL string
	// Ephemeral registers the node as ephemeral.
	Ephemeral bool

	// Direct bypasses tsnet and dials with the OS network stack. Off by
	// default; only for hosts already directly reachable.
	Direct bool

	// KnownHosts is the path to the known_hosts file.
	KnownHosts string
	// InsecureHostKey disables host-key verification (testing only).
	InsecureHostKey bool

	// IdentityFile is an optional private key for public-key auth.
	IdentityFile string

	// ShowVersion prints the version and exits.
	ShowVersion bool
	// DoUpdate checks for and installs a newer release, then exits.
	DoUpdate bool
	// NoUpdateCheck disables the best-effort update notice on startup.
	NoUpdateCheck bool
}

// Addr returns Host with a default :22 port appended if none was given.
func (c *Config) Addr() string {
	if strings.Contains(c.Host, ":") {
		return c.Host
	}
	return c.Host + ":22"
}

// Validate checks required fields and applies cross-field rules.
func (c *Config) Validate() error {
	if c.Host == "" {
		return errors.New("config: -host is required")
	}
	if c.User == "" {
		return errors.New("config: -user is required")
	}
	if !c.Direct {
		if c.Hostname == "" {
			return errors.New("config: -hostname (tsnet node name) is required unless -direct")
		}
		if c.StateDir == "" {
			return errors.New("config: -state-dir is required unless -direct (node identity must persist)")
		}
	}
	if !c.InsecureHostKey && c.KnownHosts == "" {
		return errors.New("config: -known-hosts is required unless -insecure-host-key")
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
	defKnown := ""
	if home := getenv("HOME"); home != "" {
		defKnown = filepath.Join(home, ".config", "wisp", "known_hosts")
	}

	fs.StringVar(&c.Host, "host", "", "destination host on the tailnet (host or host:port)")
	fs.StringVar(&c.User, "user", getenv("USER"), "remote login user")
	fs.StringVar(&c.Command, "command", "", "run a single command instead of a login shell")

	fs.StringVar(&c.Hostname, "hostname", "wisp", "tsnet node name advertised on the tailnet")
	fs.StringVar(&c.StateDir, "state-dir", defStateDir, "directory persisting the embedded tsnet node identity")
	fs.StringVar(&c.AuthKey, "authkey", getenv("TS_AUTHKEY"), "tsnet auth key (else interactive login URL)")
	fs.StringVar(&c.ControlURL, "control-url", getenv("TS_CONTROL_URL"), "coordination server URL (Headscale/self-hosted)")
	fs.BoolVar(&c.Ephemeral, "ephemeral", false, "register the node as ephemeral")

	fs.BoolVar(&c.Direct, "direct", false, "bypass tsnet and dial via the OS network stack (no Tailscale)")

	fs.StringVar(&c.KnownHosts, "known-hosts", defKnown, "path to known_hosts for host-key verification")
	fs.BoolVar(&c.InsecureHostKey, "insecure-host-key", false, "skip host-key verification (testing only)")
	fs.StringVar(&c.IdentityFile, "i", "", "private key file for public-key auth")

	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	fs.BoolVar(&c.DoUpdate, "update", false, "check for and install a newer release, then exit")
	fs.BoolVar(&c.NoUpdateCheck, "no-update-check", false, "do not check for updates on startup")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return c, nil
}

// MustHome returns the user home dir or panics; used to print defaults in help.
func MustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("config: cannot determine home dir: %v", err))
	}
	return h
}
