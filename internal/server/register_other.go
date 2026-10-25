//go:build !windows

package server

import (
	"fmt"
	"io"
)

// URIScheme is the protocol the offline page uses to ask the system to start
// the server again. Only the Windows build registers it.
const URIScheme = "md-localhost"

var errUnsupported = fmt.Errorf("only supported on Windows")

func SetAutostart(bool) error    { return errUnsupported }
func AutostartCommand() string   { return "" }
func SetProtocol(bool) error     { return errUnsupported }
func ProtocolRegistered() string { return "" }

func HandleURI(string, WindowMode) error { return errUnsupported }

func ShowIntegration(w io.Writer) error {
	fmt.Fprintln(w, "desktop integration is only implemented on Windows")
	return nil
}
