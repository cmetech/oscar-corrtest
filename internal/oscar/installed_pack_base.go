package oscar

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Only installed-kit adapters use this fixed route contract. Full BaseURL stays
// in original bindings; this value only interprets the transport allowlist.
type installedBase struct{ scheme, host, prefix string }

func installedBaseURL(raw, approvedPrefix string) (installedBase, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil ||
		(u.Path != "" && u.Path != approvedPrefix) || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(raw, "\\#") || strings.Contains(u.Host, "%") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || u.String() != raw {
		return installedBase{}, fmt.Errorf("installed adapter requires an exact root or %s base without aliases", approvedPrefix)
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return installedBase{}, fmt.Errorf("invalid installed base port")
		}
	}
	return installedBase{scheme: u.Scheme, host: u.Host, prefix: u.Path}, nil
}

func (base installedBase) path(request *http.Request) (string, error) {
	if request == nil || request.URL == nil || base.host == "" || request.URL.Scheme != base.scheme || request.URL.Host != base.host ||
		(request.Host != "" && request.Host != base.host) || request.URL.User != nil || request.URL.RawPath != "" || request.URL.Fragment != "" {
		return "", fmt.Errorf("installed request differs from original base")
	}
	path := request.URL.Path
	if base.prefix != "" {
		if !strings.HasPrefix(path, base.prefix+"/") {
			return "", fmt.Errorf("installed request lost original prefix")
		}
		path = strings.TrimPrefix(path, base.prefix)
	}
	return path, nil
}
