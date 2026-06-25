//go:build devmode

package service

// DevModeBuild is true when the binary is built with -tags=devmode.
// Used by main.go to gate dev-only flags (insecure CV download, dev
// intent endpoint).
const DevModeBuild = true
