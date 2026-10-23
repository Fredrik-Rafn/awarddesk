package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var publicHostname = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ConfigureDemo is startup configuration, not a live settings change. Accept
// exactly one public HTTPS address; never accept arbitrary forwarded hostnames.
func (s *Server) ConfigureDemo(address, password string) error {
	if address == "" {
		return nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !publicHostname.MatchString(u.Hostname()) || net.ParseIP(u.Hostname()) != nil || len(u.Hostname()) > 253 || strings.HasSuffix(u.Host, ":") {
		return errors.New("demo URL must be one HTTPS hostname, without a path, credentials or query")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("demo URL has an invalid port")
		}
	}
	if len(password) < 12 || len(password) > 256 {
		return errors.New("set AWARDDESK_DEMO_PASSWORD to a separate demo password of 12 to 256 bytes")
	}
	s.demoOrigin = "https://" + u.Host
	s.demoHost = u.Host
	s.demoPassword = sha256.Sum256([]byte(password))
	return nil
}

// Check the real Host header. Forwarding headers cannot turn a public request
// into a trusted localhost request or grant access to admin operations.
func (s *Server) accessOrigin(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, err := url.Parse("http://" + r.Host)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		fail(w, 403, "host is not allowed")
		return "", false
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if local {
		if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
			fail(w, 403, "forwarded requests must use the configured public hostname")
			return "", false
		}
		return "http://" + r.Host, true
	}
	if s.demoHost == "" || r.Host != s.demoHost {
		fail(w, 403, "host is not allowed")
		return "", false
	}
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		fail(w, 403, "demo access requires HTTPS")
		return "", false
	}
	if strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/api/admin") || strings.HasPrefix(r.URL.Path, "/api/logins") {
		fail(w, 403, "account management is available through localhost only")
		return "", false
	}
	user, password, ok := r.BasicAuth()
	hash := sha256.Sum256([]byte(password))
	if !ok || user != "demo" || subtle.ConstantTimeCompare(hash[:], s.demoPassword[:]) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Awarddesk demo", charset="UTF-8"`)
		fail(w, 401, "demo sign-in required")
		return "", false
	}
	return s.demoOrigin, true
}
