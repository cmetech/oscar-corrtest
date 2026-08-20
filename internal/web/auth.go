package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// SecurityMode is the explicit remote-serving authentication boundary.
type SecurityMode string

const (
	SecurityNone         SecurityMode = ""
	SecurityBearer       SecurityMode = "bearer"
	SecurityTrustedProxy SecurityMode = "trusted-proxy"
)

// Security contains resolved process-memory authentication material. It is never persisted.
type Security struct {
	Mode           SecurityMode
	BearerToken    []byte
	SecureCookies  bool
	ProxyHeader    string
	ProxyValue     string
	TrustedProxies []*net.IPNet
}

func (security Security) validate() error {
	switch security.Mode {
	case SecurityNone:
		return nil
	case SecurityBearer:
		if len(security.BearerToken) < 16 {
			return fmt.Errorf("bearer credential must contain at least 16 bytes")
		}
	case SecurityTrustedProxy:
		if security.ProxyHeader == "" || http.CanonicalHeaderKey(security.ProxyHeader) != security.ProxyHeader || security.ProxyValue == "" || len(security.TrustedProxies) == 0 {
			return fmt.Errorf("trusted-proxy mode requires a canonical identity header, exact value, and trusted proxy networks")
		}
	default:
		return fmt.Errorf("unknown remote security mode %q", security.Mode)
	}
	return nil
}

type authHandler struct {
	next     http.Handler
	security Security
}

func (handler authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if handler.security.Mode == SecurityBearer && r.URL.Path == "/login" {
		handler.login(w, r)
		return
	}
	if handler.security.Mode == SecurityBearer && r.URL.Path == "/logout" {
		handler.logout(w, r)
		return
	}
	if handler.authorized(r) {
		handler.next.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("WWW-Authenticate", `Bearer realm="oscar-corrtest"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func (handler authHandler) authorized(r *http.Request) bool {
	switch handler.security.Mode {
	case SecurityNone:
		return true
	case SecurityBearer:
		if value := r.Header.Get("Authorization"); strings.HasPrefix(value, "Bearer ") && secureEqual([]byte(strings.TrimPrefix(value, "Bearer ")), handler.security.BearerToken) {
			return true
		}
		cookie, err := r.Cookie("corrtest_session")
		return err == nil && handler.validSession(cookie.Value)
	case SecurityTrustedProxy:
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return false
		}
		ip := net.ParseIP(host)
		trusted := false
		for _, network := range handler.security.TrustedProxies {
			if network.Contains(ip) {
				trusted = true
				break
			}
		}
		return trusted && secureEqual([]byte(r.Header.Get(handler.security.ProxyHeader)), []byte(handler.security.ProxyValue))
	default:
		return false
	}
}

func (handler authHandler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Sign in · OSCAR Corrtest</title><style>body{font:16px system-ui;max-width:28rem;margin:10vh auto;padding:1rem;color:#172033;background:#f5f7fb}form{display:grid;gap:1rem;background:#fff;padding:2rem;border-radius:.75rem}input,button{font:inherit;padding:.7rem}</style><form method="post"><h1>OSCAR Corrtest</h1><label>Access token <input name="token" type="password" required autocomplete="current-password"></label><button>Sign in</button></form>`))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if !sameOrigin(r) || r.ParseForm() != nil || !secureEqual([]byte(r.FormValue("token")), handler.security.BearerToken) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(random)
	mac := hmac.New(sha256.New, handler.security.BearerToken)
	_, _ = mac.Write([]byte(nonce))
	value := nonce + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: "corrtest_session", Value: value, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: handler.security.SecureCookies, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (handler authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "invalid logout request", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "corrtest_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: handler.security.SecureCookies, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (handler authHandler) validSession(value string) bool {
	nonce, signature, ok := strings.Cut(value, ".")
	if !ok || nonce == "" || signature == "" {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, handler.security.BearerToken)
	_, _ = mac.Write([]byte(nonce))
	return hmac.Equal(provided, mac.Sum(nil))
}

func secureEqual(left, right []byte) bool {
	leftHash, rightHash := sha256.Sum256(left), sha256.Sum256(right)
	return hmac.Equal(leftHash[:], rightHash[:])
}
