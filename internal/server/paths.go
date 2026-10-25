package server

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Markdown extensions that get rendered rather than served as raw bytes.
var markdownExts = map[string]bool{
	".md":       true,
	".markdown": true,
	".mdown":    true,
	".mkd":      true,
	".mdwn":     true,
	".mdtxt":    true,
	".mdtext":   true,
	".text":     true,
}

func isMarkdown(p string) bool {
	return markdownExts[strings.ToLower(filepath.Ext(p))]
}

var driveRe = regexp.MustCompile(`^[A-Za-z]:/`)

// pathToURLPath encodes an absolute filesystem path as the tail of a /view/
// URL. Keeping the real directory structure in the URL means relative links
// and relative image sources inside a document resolve correctly in the
// browser with no rewriting at all.
func pathToURLPath(abs string) string {
	s := filepath.ToSlash(abs)
	if strings.HasPrefix(s, "//") { // UNC share: //server/share/...
		return "unc/" + strings.TrimPrefix(s, "//")
	}
	return strings.TrimPrefix(s, "/")
}

// urlPathToPath is the inverse of pathToURLPath.
func urlPathToPath(rest string) (string, error) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(rest, "unc/") {
		return filepath.FromSlash("//" + strings.TrimPrefix(rest, "unc/")), nil
	}
	if runtime.GOOS == "windows" {
		if !driveRe.MatchString(rest) {
			return "", fmt.Errorf("expected a drive-qualified path, got %q", rest)
		}
		return filepath.FromSlash(rest), nil
	}
	return filepath.FromSlash("/" + rest), nil
}

// viewPath is the site-relative URL that displays a file.
func viewPath(abs string) string {
	return (&url.URL{Path: "/view/" + pathToURLPath(abs)}).String()
}

// viewURL builds the full browser URL for a file.
func viewURL(host string, port int, abs string) string {
	u := &url.URL{Path: "/view/" + pathToURLPath(abs)}
	return displayOrigin(host, port) + u.String()
}

// withinRoot reports whether target sits inside root (or is root itself).
func withinRoot(root, target string) bool {
	if runtime.GOOS == "windows" {
		// Windows paths are case-insensitive, but filepath.Rel is not, so a
		// link that differs only in case would otherwise be refused.
		root = strings.ToLower(root)
		target = strings.ToLower(target)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
