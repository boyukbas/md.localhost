//go:build windows

package server

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = AppName
	// URIScheme is the protocol the offline page uses to ask Windows to start
	// the server again, so a pinned icon can recover without a terminal.
	URIScheme = "md-localhost"
)

// SetAutostart makes the server start at logon and never go idle, so a taskbar
// icon always has something to open. Everything is written under HKCU.
func SetAutostart(on bool) error {
	if !on {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if err != nil {
			return nil // no Run key at all: nothing to remove
		}
		defer k.Close()
		if err := k.DeleteValue(runValueName); err != nil && !os.IsNotExist(err) {
			return nil // already absent
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	// Pin the port explicitly: an installed app is identified by its origin,
	// so a logon server that picked a different port would leave the pinned
	// icon pointing at nothing.
	port := RememberedPort()
	if port == 0 {
		port = DefaultPort
	}
	cmd := fmt.Sprintf(`"%s" serve --idle 0 --port %d`, exe, port)
	return k.SetStringValue(runValueName, cmd)
}

// AutostartCommand returns the logon command currently registered, if any.
func AutostartCommand() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValueName)
	if err != nil {
		return ""
	}
	return v
}

// SetProtocol registers or removes the md-localhost: URI scheme, which is what
// lets the offline page start the server again with one click.
func SetProtocol(on bool) error {
	base := `Software\Classes\` + URIScheme
	if !on {
		// Delete depth first: a key with subkeys cannot be removed.
		for _, p := range []string{base + `\shell\open\command`, base + `\shell\open`, base + `\shell`, base + `\DefaultIcon`, base} {
			_ = registry.DeleteKey(registry.CURRENT_USER, p)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", "URL:"+AppName); err != nil {
		return err
	}
	// The empty "URL Protocol" value is what marks a class as a URI scheme.
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	ik, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\DefaultIcon`, registry.SET_VALUE)
	if err == nil {
		_ = ik.SetStringValue("", exe+",0")
		ik.Close()
	}

	ck, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer ck.Close()
	return ck.SetStringValue("", `"`+exe+`" uri "%1"`)
}

// ProtocolRegistered reports the command registered for the URI scheme, if any.
func ProtocolRegistered() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+URIScheme+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if err != nil {
		return ""
	}
	return v
}

// HandleURI acts on an md-localhost: URI. The one the offline page uses is
// md-localhost:start, which only has to bring the server back.
func HandleURI(raw string, mode WindowMode) error {
	action := strings.TrimPrefix(raw, URIScheme+":")
	action = strings.Trim(action, "/")
	switch action {
	case "", "start":
		_, err := ensureServer()
		return err
	case "home":
		return OpenHome(mode)
	default:
		return fmt.Errorf("unknown %s action %q", URIScheme, action)
	}
}

// ShowIntegration prints where the viewer has hooked into Windows.
func ShowIntegration(w io.Writer) error {
	if cmd := AutostartCommand(); cmd != "" {
		fmt.Fprintf(w, "autostart: on  (%s)\n", cmd)
	} else {
		fmt.Fprintln(w, "autostart: off")
	}
	if cmd := ProtocolRegistered(); cmd != "" {
		fmt.Fprintf(w, "protocol:  %s: -> %s\n", URIScheme, cmd)
	} else {
		fmt.Fprintf(w, "protocol:  %s: not registered\n", URIScheme)
	}
	if lnk := installedAppShortcut(); lnk != "" {
		fmt.Fprintf(w, "installed: yes (%s)\n", lnk)
	} else {
		fmt.Fprintln(w, "installed: no  (open the viewer, then Chrome menu > Cast, save and share > Install page as app)")
	}
	return nil
}
