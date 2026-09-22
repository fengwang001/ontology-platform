// Package host normalizes the authority part of a URL: reg-name case,
// trailing dots, IPv6 literal compression and port form. Default-port
// elimination is left to the caller, which knows the scheme.
package host

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

var (
	// ErrEmptyHost: the host part is empty (or only dots).
	ErrEmptyHost = errors.New("host: empty host")
	// ErrBadIPv6: malformed bracketed IPv6 literal.
	ErrBadIPv6 = errors.New("host: invalid IPv6 literal")
	// ErrBadPort: port is not a number in 0..65535.
	ErrBadPort = errors.New("host: invalid port")
	// ErrUserinfo: userinfo is rejected, not normalized.
	ErrUserinfo = errors.New("host: userinfo not supported")
)

// Normalize parses authority and returns the normalized host and port.
// hasPort is false when no port was present or the port was empty
// ("host:" is equivalent to "host").
func Normalize(authority string) (host, port string, hasPort bool, err error) {
	if strings.IndexByte(authority, '@') >= 0 {
		return "", "", false, ErrUserinfo
	}
	if strings.HasPrefix(authority, "[") {
		host, port, hasPort, err = splitLiteral(authority)
	} else {
		host, port, hasPort, err = splitRegName(authority)
	}
	if err != nil {
		return "", "", false, err
	}
	if !hasPort || port == "" {
		return host, "", false, nil
	}
	norm, err := normPort(port)
	if err != nil {
		return "", "", false, err
	}
	return host, norm, true, nil
}

func splitLiteral(a string) (host, port string, hasPort bool, err error) {
	end := strings.IndexByte(a, ']')
	if end < 0 {
		return "", "", false, ErrBadIPv6
	}
	addr, aerr := netip.ParseAddr(a[1:end])
	if aerr != nil || addr.Is4() {
		return "", "", false, ErrBadIPv6
	}
	host = "[" + addr.String() + "]"
	rest := a[end+1:]
	if rest == "" {
		return host, "", false, nil
	}
	if rest[0] != ':' {
		return "", "", false, ErrBadIPv6
	}
	return host, rest[1:], true, nil
}

func splitRegName(a string) (host, port string, hasPort bool, err error) {
	if strings.Count(a, ":") > 1 {
		return "", "", false, ErrBadIPv6
	}
	host = a
	if i := strings.IndexByte(a, ':'); i >= 0 {
		host, port, hasPort = a[:i], a[i+1:], true
	}
	host = strings.TrimRight(strings.ToLower(host), ".")
	if host == "" {
		return "", "", false, ErrEmptyHost
	}
	return host, port, hasPort, nil
}

func normPort(p string) (string, error) {
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return "", ErrBadPort
		}
	}
	p = strings.TrimLeft(p, "0")
	if p == "" {
		return "0", nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n > 65535 {
		return "", ErrBadPort
	}
	return p, nil
}
