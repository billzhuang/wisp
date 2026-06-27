//go:build !ebiten

package main

// assetFlavor is the release-asset prefix this build self-updates from. The
// default (stdio/CLI) build pulls "wisp_<os>_<arch>".
const assetFlavor = "wisp"
