// Package host normalizes the authority (host[:port]) part of a URL.
package host

import (
	"fmt"
	"net"
	"strings"
)

// Changes records which kinds of rewrites were applied, so callers can
// report them without re-comparing strings.
type Changes struct {
	Case        bool // host case folded
	TrailingDot bool // trailing dot(s) removed
	DefaultPort bool // default port removed
	PortSyntax  bool // empty port dropped or leading zeros stripped
	IPv6        bool // IPv6 literal recompressed
}

// Error is a distinguishable authority syntax error.
type Error struct{ Msg string }

func (e *Error) Error() string { return "host: " + e.Msg }

var defaultPorts = map[string]string{"http": "80", "https": "443"}

// Normalize splits and normalizes an authority string. scheme is used only
// for default-port elimination. It returns the canonical host (IPv6
// literals keep their brackets) and the canonical port ("" if absent).
func Normalize(authority, scheme string) (host, port string, ch Changes, err error) {
	if authority == "" {
		return "", "", ch, &Error{Msg: "empty host"}
	}
	var rawHost, rawPort string
	hasPort := false
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", "", ch, &Error{Msg: "unterminated IPv6 literal"}
		}
		rawHost = authority[:end+1]
		rest := authority[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", "", ch, &Error{Msg: "junk after IPv6 literal"}
			}
			rawPort, hasPort = rest[1:], true
		}
	} else {
		if i := strings.IndexByte(authority, ':'); i >= 0 {
			rawHost, rawPort, hasPort = authority[:i], authority[i+1:], true
			if strings.Contains(rawPort, ":") {
				return "", "", ch, &Error{Msg: "bare IPv6 address must use brackets"}
			}
		} else {
			rawHost = authority
		}
	}
	host, ch, err = normalizeHost(rawHost)
	if err != nil {
		return "", "", ch, err
	}
	if !hasPort {
		return host, "", ch, nil
	}
	port, ch2, err := normalizePort(rawPort, scheme)
	if err != nil {
		return "", "", ch, err
	}
	ch.DefaultPort = ch.DefaultPort || ch2.DefaultPort
	ch.PortSyntax = ch.PortSyntax || ch2.PortSyntax
	return host, port, ch, nil
}

func normalizeHost(raw string) (string, Changes, error) {
	var ch Changes
	if strings.HasPrefix(raw, "[") {
		inner := raw[1 : len(raw)-1]
		ip := net.ParseIP(inner)
		if ip == nil || ip.To4() != nil {
			return "", ch, &Error{Msg: fmt.Sprintf("invalid IPv6 literal %q", inner)}
		}
		canon := "[" + ip.String() + "]"
		ch.IPv6 = canon != raw
		return canon, ch, nil
	}
	lower := strings.ToLower(raw)
	ch.Case = lower != raw
	trimmed := strings.TrimRight(lower, ".")
	ch.TrailingDot = trimmed != lower
	if trimmed == "" {
		return "", ch, &Error{Msg: "empty host"}
	}
	return trimmed, ch, nil
}

func normalizePort(raw, scheme string) (string, Changes, error) {
	var ch Changes
	if raw == "" {
		ch.PortSyntax = true // "host:" is equivalent to no port
		return "", ch, nil
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return "", ch, &Error{Msg: fmt.Sprintf("invalid port %q", raw)}
		}
	}
	p := strings.TrimLeft(raw, "0")
	if p == "" {
		p = "0"
	}
	if p != raw {
		ch.PortSyntax = true
	}
	if len(p) > 5 || (len(p) == 5 && p > "65535") {
		return "", ch, &Error{Msg: fmt.Sprintf("port %q out of range", raw)}
	}
	if def, ok := defaultPorts[scheme]; ok && p == def {
		ch.DefaultPort = true
		return "", ch, nil
	}
	return p, ch, nil
}
