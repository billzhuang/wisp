package sshx

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsCallback returns a HostKeyCallback backed by an OpenSSH-style
// known_hosts file at path. If trustOnFirstUse is set, a host whose key is not
// yet recorded is accepted once and appended to the file (TOFU); a host whose
// recorded key has *changed* is always rejected, since that is the signature of
// a man-in-the-middle. If the file does not exist it is created.
func KnownHostsCallback(path string, trustOnFirstUse bool) (ssh.HostKeyCallback, error) {
	if path == "" {
		return nil, fmt.Errorf("sshx: known_hosts path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Close()
		} else {
			return nil, err
		}
	}

	base, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !trustOnFirstUse {
			return err
		}
		// A KeyError with a populated Want slice means the host is known but the
		// key differs — never auto-trust that.
		if asKeyError(err, &keyErr) && len(keyErr.Want) > 0 {
			return fmt.Errorf("sshx: host key mismatch for %s (possible MITM): %w", hostname, err)
		}
		// Otherwise the host is simply unknown: record it and accept.
		mu.Lock()
		defer mu.Unlock()
		return appendKnownHost(path, hostname, key)
	}, nil
}

func asKeyError(err error, target **knownhosts.KeyError) bool {
	if ke, ok := err.(*knownhosts.KeyError); ok {
		*target = ke
		return true
	}
	return false
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	_, err = fmt.Fprintln(f, line)
	return err
}

// InsecureIgnoreHostKey returns a callback that accepts any host key. It exists
// only for tests and explicitly-opted-in throwaway use; never wire it to the
// default path.
func InsecureIgnoreHostKey() ssh.HostKeyCallback {
	return ssh.InsecureIgnoreHostKey()
}
