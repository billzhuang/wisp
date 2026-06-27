//go:build ebiten

package main

// assetFlavor is the release-asset prefix this build self-updates from. The
// Ebitengine GUI build pulls "wisp-gui_<os>_<arch>", so a GUI install upgrades
// to a GUI binary and never to the headless CLI one.
const assetFlavor = "wisp-gui"
