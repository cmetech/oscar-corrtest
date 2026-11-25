package score

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// AlertSource and AlertSourceIP are what OSCAR's Alert View Source and Source
// IP columns show, and what its uisrc:source / uisrc:source_ip filters match
// (OSC-159). A port of oscar-alertmanager src/app/core/alert_source.py
// (derive_source, derive_source_ip, url_host, the columns() clip), itself a
// port of oscar-adminui src/lib/alertSource.js; testdata holds the shared
// vectors both sides pin. Labels are a map: a duplicate label name (not
// possible from a Prometheus label set) has no "first" value here.

var (
	ipv4Shape = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	ipv4      = regexp.MustCompile(`^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$`)
)

const (
	sourceMax   = 255 // alert_source VARCHAR(255), in characters
	sourceIPMax = 15
)

// jsTrim is JS String.prototype.trim: WhiteSpace (TAB, VT, FF, ZWNBSP, every
// Zs) and LineTerminator (LF, CR, LS, PS).
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\v', '\f', '\ufeff', '\n', '\r', '\u2028', '\u2029':
			return true
		}
		return unicode.Is(unicode.Zs, r)
	})
}

// urlHost is the host of scheme://[user@]host[:port][/path][?q][#f].
func urlHost(v string) string {
	rest := v[strings.Index(v, "://")+3:]
	authority := strings.Split(strings.Split(strings.Split(rest, "/")[0], "?")[0], "#")[0]
	hostPort := authority[strings.LastIndex(authority, "@")+1:]
	if strings.HasPrefix(hostPort, "[") && strings.Contains(hostPort, "]") {
		return hostPort[:strings.Index(hostPort, "]")+1]
	}
	return strings.Split(hostPort, ":")[0]
}

func validIPv4(v string) bool {
	m := ipv4.FindStringSubmatch(jsTrim(v))
	if m == nil {
		return false
	}
	for _, o := range m[1:] {
		if n, _ := strconv.Atoi(o); n > 255 {
			return false
		}
	}
	return true
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// AlertSource: meta_hostname (an IPv4-shaped value whole, else the part
// before the first dot), else meta_ipaddress as stored, else the instance
// host (URL host for scheme://..., else the part before ':').
func AlertSource(labels map[string]string) string {
	return clip(alertSource(labels), sourceMax)
}

func alertSource(labels map[string]string) string {
	if h := labels["meta_hostname"]; h != "" {
		if ipv4Shape.MatchString(h) {
			return h
		}
		if short := strings.Split(h, ".")[0]; short != "" {
			return short
		}
		return h
	}
	if ip := labels["meta_ipaddress"]; ip != "" {
		return ip
	}
	inst := labels["instance"]
	switch {
	case inst == "":
		return ""
	case strings.Contains(inst, "://"):
		return urlHost(inst)
	case strings.Contains(inst, ":"):
		if h := strings.Split(inst, ":")[0]; h != "" {
			return h
		}
	}
	return inst
}

// AlertSourceIP: the first of meta_ipaddress, the instance host and node that
// is a valid IPv4 after a JS trim, trimmed.
func AlertSourceIP(labels map[string]string) string {
	return clip(alertSourceIP(labels), sourceIPMax)
}

func alertSourceIP(labels map[string]string) string {
	if ip := labels["meta_ipaddress"]; validIPv4(ip) {
		return jsTrim(ip)
	}
	if inst := labels["instance"]; inst != "" {
		host := jsTrim(inst)
		switch {
		case strings.Contains(host, "://"):
			host = urlHost(host)
		case strings.Contains(host, ":"):
			host = strings.Split(host, ":")[0]
		}
		if validIPv4(host) {
			return jsTrim(host)
		}
	}
	if n := labels["node"]; validIPv4(n) {
		return jsTrim(n)
	}
	return ""
}

// SourceActions are the 3ps v3 front-office Source / Source IP filter
// actions (FrontOfficeSpec.SourceFilters), each an OSCAR uisrc filter.
var SourceActions = []string{"source_equals", "source_contains", "source_ip_startswith", "source_ip_notempty"}

// SourceFilter is the uisrc filter field and MUI operator of a source action.
func SourceFilter(action string) (field, operator string) {
	switch action {
	case "source_equals":
		return "uisrc:source", "equals"
	case "source_contains":
		return "uisrc:source", "contains"
	case "source_ip_startswith":
		return "uisrc:source_ip", "startsWith"
	case "source_ip_notempty":
		return "uisrc:source_ip", "isNotEmpty"
	}
	return "", ""
}

// MatchesSourceFilter reports whether an alert with these labels matches the
// action's filter by OSCAR's rule (the shown value; equals and contains /
// startsWith case-insensitive: utf8mb4_0900_ai_ci / _as_ci on these ASCII
// values; isNotEmpty: a value is shown).
func MatchesSourceFilter(action, value string, labels map[string]string) bool {
	src, ip := strings.ToLower(AlertSource(labels)), strings.ToLower(AlertSourceIP(labels))
	value = strings.ToLower(value)
	switch action {
	case "source_equals":
		return src == value
	case "source_contains":
		return strings.Contains(src, value)
	case "source_ip_startswith":
		return strings.HasPrefix(ip, value)
	case "source_ip_notempty":
		return ip != ""
	}
	return false
}
