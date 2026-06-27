// Package banner renders wisp's ASCII startup logo. It is intentionally a
// couple of constant strings and a single Sprintf — printing it costs nothing
// measurable, so it never slows startup.
package banner

import "fmt"

// Logo is the wisp wordmark (figlet "standard").
const Logo = `__        ___ ____  ____
\ \      / (_) ___||  _ \
 \ \ /\ / /| \___ \| |_) |
  \ V  V / | |___) |  __/
   \_/\_/  |_|____/|_|`

// Render returns the logo plus a one-line tagline and the build version, ready
// to print. status is a short phrase shown after the version (e.g. "connecting
// to dev-box:22").
func Render(version, status string) string {
	out := "\n" + Logo + "\n  tailnet-native terminal · " + version + "\n"
	if status != "" {
		out += "  " + status + "\n"
	}
	return out
}

// Lines returns the logo split into rows, for renderers that draw text cell by
// cell (the GUI splash).
func Lines() []string {
	return []string{
		`__        ___ ____  ____`,
		`\ \      / (_) ___||  _ \`,
		` \ \ /\ / /| \___ \| |_) |`,
		`  \ V  V / | |___) |  __/`,
		`   \_/\_/  |_|____/|_|`,
	}
}

// Tagline is the short descriptor drawn under the logo.
func Tagline(version string) string {
	return fmt.Sprintf("tailnet-native terminal · %s", version)
}
