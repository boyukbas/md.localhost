//go:build windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// WindowMode decides how a viewer is opened when none is running yet.
type WindowMode int

const (
	// WindowAuto uses a dedicated app window when the default browser is a
	// Chromium one, and an ordinary browser tab otherwise.
	WindowAuto WindowMode = iota
	// WindowApp insists on a dedicated window, using any Chromium browser it
	// can find.
	WindowApp
	// WindowBrowser opens a tab in the default browser.
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

// openViewer shows a URL in a new viewer window or browser tab. It reports
// whether it launched the installed application, which opens on its own start
// page rather than on the requested URL — the caller then hands the document
// over once that window connects.
func openViewer(u string, mode WindowMode, useInstalled bool) (bool, error) {
	// An installed app has its own taskbar identity and icon, and is what the
	// user pinned. Reusing it keeps every viewer window under that one button
	// instead of scattering --app windows beside it.
	if useInstalled && mode != WindowBrowser {
		if lnk := installedAppShortcut(); lnk != "" {
			if err := shellOpen(lnk); err == nil {
				return true, nil
			}
		}
	}
	switch mode {
	case WindowBrowser:
		return false, openInBrowser(u)
	case WindowApp:
		if exe := chromiumBrowser(true); exe != "" {
			return false, launchAppWindow(exe, u)
		}
		return false, openInBrowser(u)
	default:
		if exe := chromiumBrowser(false); exe != "" {
			return false, launchAppWindow(exe, u)
		}
		return false, openInBrowser(u)
	}
}

// installedAppShortcut returns the shortcut Chrome writes when the viewer is
// installed as an application, or "" when it is not installed. Chrome keeps web
// app shortcuts in a folder of their own, so anything found there is an
// installed app and never this program's own Start-menu entry — launching that
// by mistake would put the launcher in a loop.
func installedAppShortcut() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	p := filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\Chrome Apps`, AppName+".lnk")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// shellOpen launches a shortcut the way Explorer would, which is the only way
// to start it with the application identity Windows groups the taskbar by.
func shellOpen(path string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(target)),
		0, 0,
		uintptr(swShowNormal),
	)
	// ShellExecuteW returns a value greater than 32 on success. Anything else
	// is an error code, not a handle.
	if r <= 32 {
		return fmt.Errorf("could not launch %s: ShellExecute returned %d (%v)", path, r, callErr)
	}
	return nil
}

// launchAppWindow opens a Chromium browser in app mode: one window per viewer,
// no browser tab strip and no address bar, so md.localhost behaves like an
// application whose tabs are the ones it draws itself.
func launchAppWindow(exe, u string) error {
	cmd := exec.Command(exe, "--app="+u, "--window-size=1280,900")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// chromiumBrowser returns the executable of a Chromium browser to host the
// viewer window. Unless anyInstalled is set it only considers the browser the
// user has actually chosen as their default.
func chromiumBrowser(anyInstalled bool) string {
	if exe := defaultBrowserExe(); exe != "" && isChromium(exe) {
		return exe
	}
	if !anyInstalled {
		return ""
	}
	roots := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	}
	rel := []string{
		`Google\Chrome\Application\chrome.exe`,
		`Microsoft\Edge\Application\msedge.exe`,
		`BraveSoftware\Brave-Browser\Application\brave.exe`,
		`Vivaldi\Application\vivaldi.exe`,
	}
	for _, r := range rel {
		for _, root := range roots {
			if root == "" {
				continue
			}
			p := filepath.Join(root, r)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// defaultBrowserExe reads the user's chosen handler for http:// links.
func defaultBrowserExe() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`SOFTWARE\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`,
		registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	progID, _, err := k.GetStringValue("ProgId")
	if err != nil || progID == "" {
		return ""
	}

	ck, err := registry.OpenKey(registry.CLASSES_ROOT, progID+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer ck.Close()
	cmd, _, err := ck.GetStringValue("")
	if err != nil {
		return ""
	}
	return exeFromCommand(cmd)
}

// exeFromCommand pulls the program out of a registered shell command line.
func exeFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.Index(cmd[1:], `"`); i > 0 {
			return cmd[1 : 1+i]
		}
		return ""
	}
	if i := strings.Index(cmd, " "); i > 0 {
		return cmd[:i]
	}
	return cmd
}

func isChromium(exe string) bool {
	switch strings.ToLower(filepath.Base(exe)) {
	case "chrome.exe", "msedge.exe", "brave.exe", "vivaldi.exe", "opera.exe", "thorium.exe":
		return true
	}
	return false
}

// ---------------------------------------------------------------- focus

const swShowNormal = 1

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")

	user32                  = syscall.NewLazyDLL("user32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSwitchToThisWindow  = user32.NewProc("SwitchToThisWindow")
)

const swRestore = 9

// focusViewer brings an already-open viewer window to the front. The page
// itself cannot do this — a background browser tab has no way to raise its own
// window — so the launcher hunts for the window by its title, which every page
// ends with titleSuffix precisely so that this works.
//
// It is best effort: Windows only lets the foreground be taken by a process
// that is already interacting with the user, which a double-clicked file
// handler is, but a browser window showing a different tab cannot be matched
// at all because its title is that other tab's.
func focusViewer(preferTitle string) bool {
	var exact, any uintptr

	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		title := windowText(hwnd)
		if !strings.Contains(title, titleSuffix) {
			return 1
		}
		if any == 0 {
			any = hwnd
		}
		if exact == 0 && preferTitle != "" && strings.HasPrefix(title, preferTitle) {
			exact = hwnd
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)

	hwnd := exact
	if hwnd == 0 {
		hwnd = any
	}
	if hwnd == 0 {
		return false
	}

	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	if ok, _, _ := procSetForegroundWindow.Call(hwnd); ok != 0 {
		return true
	}
	// SetForegroundWindow refuses when this process does not hold the
	// foreground lock; SwitchToThisWindow is the shell's own way round it.
	procSwitchToThisWindow.Call(hwnd, 1)
	return true
}

func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
