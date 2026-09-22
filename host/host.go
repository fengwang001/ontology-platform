// Package host normalizes the authority component of a URL: host case, the
// trailing root dot, redundant port forms, and IPv6 literal brackets and
// zero-segment compression. It depends on no other package.
package host

import (
	"errors"
	"strings"
)

var (
	// ErrMalformedHost means the authority cannot be parsed.
	ErrMalformedHost = errors.New("host: malformed authority")
	// ErrBadPort means a port is present but not a decimal number in 0..65535.
	ErrBadPort = errors.New("host: invalid port")
	// ErrBadIPv6 means a bracketed literal is not a valid IPv6 address.
	ErrBadIPv6 = errors.New("host: invalid IPv6 literal")
)

// Rewrite flags describe which host normalizations were applied.
const (
	RewriteCase     = 1 << iota // host letters lower-cased
	RewriteTrailingDot          // terminal root dot removed
	RewritePortZero             // leading-zero port normalized
	RewriteIPv6                 // IPv6 groups zero-padded/compressed/bracketed
)

// Host is a normalized authority.
type Host struct {
	Host     string // canonical host without brackets or port
	IsIPv6   bool
	Port     string // canonical port digits; "" when absent or empty
	Rewrites int
}

// Normalize parses and normalizes authority. defaultPort is the canonical
// default port for the scheme (e.g. "80"); when the explicit port equals it
// the port is dropped (that drop itself is not flagged here; the caller owns
// scheme semantics).
func Normalize(authority, defaultPort string) (Host, error) {
	if authority == "" {
		return Host{}, ErrMalformedHost
	}
	h := Host{}
	body := authority
	if body[0] == '[' {
		end := strings.IndexByte(body, ']')
		if end < 0 || end == 1 {
			return Host{}, ErrBadIPv6
		}
		canon, err := normalizeIPv6(body[1:end])
		if err != nil {
			return Host{}, err
		}
		h.Host = canon
		h.IsIPv6 = true
		h.Rewrites |= RewriteIPv6
		rest := body[end+1:]
		if rest != "" {
			if rest[0] != ':' || rest == ":" {
				return Host{}, ErrMalformedHost
			}
			if err := h.setPort(rest[1:], defaultPort); err != nil {
				return Host{}, err
			}
		}
		return h, nil
	}
	if strings.ContainsAny(authority, "[]") {
		return Host{}, ErrBadIPv6
	}
	name := authority
	port := ""
	if ci := strings.LastIndexByte(authority, ':'); ci >= 0 {
		name, port = authority[:ci], authority[ci+1:]
	}
	if name == "" {
		return Host{}, ErrMalformedHost
	}
	canonName, nameChanges := normalizeRegName(name)
	h.Host = canonName
	if nameChanges&rewriteCase != 0 {
		h.Rewrites |= RewriteCase
	}
	if nameChanges&rewriteTrailingDot != 0 {
		h.Rewrites |= RewriteTrailingDot
	}
	if err := h.setPort(port, defaultPort); err != nil {
		return Host{}, err
	}
	return h, nil
}

func (h *Host) setPort(port, defaultPort string) error {
	if port == "" {
		h.Port = "" // "host:" is equivalent to no port
		return nil
	}
	digits := port
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ErrBadPort
		}
	}
	trimmed := strings.TrimLeft(digits, "0")
	if trimmed == "" {
		trimmed = "0"
	}
	if len(trimmed) > 5 || numericGreater(trimmed, "65535") {
		return ErrBadPort
	}
	if trimmed != digits {
		h.Rewrites |= RewritePortZero
	}
	if trimmed == defaultPort {
		h.Port = ""
		return nil
	}
	h.Port = trimmed
	return nil
}

func numericGreater(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

// Authority renders the canonical authority, re-adding IPv6 brackets.
func (h Host) Authority() string {
	if h.IsIPv6 {
		if h.Port != "" {
			return "[" + h.Host + "]:" + h.Port
		}
		return "[" + h.Host + "]"
	}
	if h.Port != "" {
		return h.Host + ":" + h.Port
	}
	return h.Host
}
