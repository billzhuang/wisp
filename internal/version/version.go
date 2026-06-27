// Package version exposes the build version of wisp. The release version is
// injected at link time by the CD pipeline:
//
//	go build -ldflags "-X github.com/billzhuang/wisp/internal/version.Version=1.2.3"
//
// Development builds report "dev", which the updater treats as "always older"
// so a local build never thinks it is up to date with a release.
package version

// Version is the current build version, without a leading "v". Overridden at
// link time for releases.
var Version = "dev"

// Repo is the GitHub "owner/name" the updater checks for new releases.
const Repo = "billzhuang/wisp"

// IsDev reports whether this is an unversioned development build.
func IsDev() bool { return Version == "dev" || Version == "" }

// Current returns the current version string.
func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
