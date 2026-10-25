//go:build !windows

package server

import "strings"

// WindowMode decides how a viewer is opened when none is running yet. Only
// Windows has the dedicated app window; elsewhere every mode is a browser tab.
type WindowMode int

const (
	WindowAuto WindowMode = iota
	WindowApp
	WindowBrowser
)

// ParseWindowMode maps a flag or MDL_WINDOW value onto a mode.
func ParseWindowMode(s string) WindowMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "app", "window":
		return WindowApp
	case "browser", "tab":
		return WindowBrowser
	default:
		return WindowAuto
	}
}

func openViewer(u string, _ WindowMode, _ bool) (bool, error) { return false, openInBrowser(u) }

// installedAppShortcut has no non-Windows equivalent.
func installedAppShortcut() string { return "" }

// focusViewer has no portable equivalent; the page still receives the document.
func focusViewer(string) bool { return false }
