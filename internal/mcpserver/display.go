package mcpserver

import (
	"net/url"
	"strings"
)

// DisplayRepo returns a human-friendly label for a repo URL.
// For GitHub URLs it returns "owner/repo"; for other git hosts it returns
// "host/path"; for local paths it returns the path as-is.
func DisplayRepo(rawURL, sourceType string) string {
	if rawURL == "" {
		return ""
	}
	if sourceType == "" {
		if strings.HasPrefix(rawURL, "/") || (len(rawURL) >= 2 && rawURL[1] == ':') {
			sourceType = "local"
		} else {
			sourceType = "git"
		}
	}
	if sourceType == "local" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	if u.Hostname() == "github.com" {
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	host := u.Host
	if path == "" {
		return host
	}
	return host + "/" + path
}
