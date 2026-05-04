//go:build !devmode

package service

// DevModeBuild is false in production builds. Used by main.go to
// hard-fail at boot when dev-only flags are set against a non-devmode
// binary, so a developer can't accidentally ship to prod with the CV
// SSRF guard disabled.
const DevModeBuild = false
