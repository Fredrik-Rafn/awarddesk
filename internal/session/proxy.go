package session

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var proxyScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

func ParseProxy(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "http://") && !strings.Contains(raw, "@") {
		export := strings.TrimPrefix(raw, "http://")
		if !strings.HasPrefix(export, "[") && len(strings.SplitN(export, ":", 4)) == 4 {
			raw = export
		}
	}
	var u *url.URL
	var e error
	if proxyScheme.MatchString(raw) {
		u, e = url.Parse(raw)
	} else {
		p := strings.SplitN(raw, ":", 4)
		if len(p) != 4 {
			return "", errors.New("proxy must be http://user:password@host:port or host:port:user:password")
		}
		u = &url.URL{Scheme: "http", Host: net.JoinHostPort(p[0], p[1]), User: url.UserPassword(p[2], p[3])}
	}
	if e != nil || u == nil || u.Scheme != "http" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid HTTP proxy configuration")
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n/\\") {
		return "", errors.New("invalid HTTP proxy host")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return "", errors.New("proxy port must be between 1 and 65535")
	}
	if u.User != nil {
		pass, ok := u.User.Password()
		if u.User.Username() == "" || !ok || pass == "" {
			return "", errors.New("proxy username and password are required")
		}

		for _, r := range u.User.Username() + pass {
			if r < 32 || r == 127 {
				return "", errors.New("proxy credentials contain control characters")
			}
		}
	}
	return u.String(), nil
}
