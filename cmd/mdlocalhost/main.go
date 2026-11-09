// Command mdlocalhost opens Markdown files, rendered, at http://md.localhost/.
//
// It is what the .md file association runs (`mdlocalhost open <file>`), and it
// doubles as a small CLI for the background server.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/boyukbas/md.localhost/internal/server"
)

const usage = `mdlocalhost: Markdown files, rendered, at http://md.localhost/

Usage:
  mdlocalhost open [--new] [--window auto|app|browser] [--host H] <file>
                                  Add a tab to the open viewer, or start one.
  mdlocalhost home [--window ...]   Open the list of recently viewed files.
  mdlocalhost serve [--port N] [--host H] [--idle MIN]
                                  Run the server in the foreground.
  mdlocalhost status              Where the server is listening, and since when.
  mdlocalhost stop                Shut the server down now.
  mdlocalhost autostart on|off    Start the server at logon and never idle out.
  mdlocalhost protocol on|off     Register the md-localhost: link the offline page uses.
  mdlocalhost uri <md-localhost:...>
                                  Handle a md-localhost: link (what the protocol runs).

The window mode can also be set with the MDL_WINDOW environment variable.
`

func main() {
	server.AttachParentConsole()
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mdlocalhost:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]

	// A bare path (e.g. a file dropped onto the .exe) means "open it".
	if !strings.HasPrefix(cmd, "-") && !isCommand(cmd) {
		cmd, rest = "open", args
	}

	switch cmd {
	case "open":
		fs := flag.NewFlagSet("open", flag.ContinueOnError)
		newViewer := fs.Bool("new", false, "ignore the open viewer and start another one")
		window := fs.String("window", os.Getenv("MDL_WINDOW"), "auto, app or browser")
		host := fs.String("host", "", "host name to use in the viewer's address")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("open takes exactly one file")
		}
		abs, err := filepath.Abs(fs.Arg(0))
		if err != nil {
			return err
		}
		return server.Open(abs, *host, server.ParseWindowMode(*window), !*newViewer)

	case "home":
		fs := flag.NewFlagSet("home", flag.ContinueOnError)
		window := fs.String("window", os.Getenv("MDL_WINDOW"), "auto, app or browser")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return server.OpenHome(server.ParseWindowMode(*window))

	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		port := fs.Int("port", server.DefaultPort, "preferred port")
		host := fs.String("host", server.DefaultHost, "host name to serve")
		idle := fs.Int("idle", server.DefaultIdleMinutes, "minutes to linger with no open viewers (0 = never stop)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return server.Serve(server.Config{PreferredPort: *port, Host: *host, IdleMinutes: *idle})

	case "status":
		if err := server.Status(os.Stdout); err != nil {
			return err
		}
		return server.ShowIntegration(os.Stdout)

	case "stop":
		return server.Stop(os.Stdout)

	case "autostart":
		on, err := onOff(rest)
		if err != nil {
			return err
		}
		return server.SetAutostart(on)

	case "protocol":
		on, err := onOff(rest)
		if err != nil {
			return err
		}
		return server.SetProtocol(on)

	case "uri":
		if len(rest) != 1 {
			return fmt.Errorf("uri takes exactly one link")
		}
		return server.HandleURI(rest[0], server.ParseWindowMode(os.Getenv("MDL_WINDOW")))

	case "help", "-h", "--help", "/?":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (run without arguments for help)", cmd)
}

func isCommand(s string) bool {
	switch s {
	case "open", "home", "serve", "status", "stop", "autostart", "protocol", "uri", "help":
		return true
	}
	return false
}

func onOff(args []string) (bool, error) {
	if len(args) == 1 {
		switch strings.ToLower(args[0]) {
		case "on":
			return true, nil
		case "off":
			return false, nil
		}
	}
	return false, fmt.Errorf("expected on or off")
}
