package host

import (
	"strconv"
	"strings"

	"ontology/pct"
)

// Host is a normalized authority host with optional port.
type Host struct {
	name string // without brackets for IPv6
	ip6  bool
	port string // "" when absent
}

// Parse normalizes an authority's host[:port] text.
func Parse(raw string, defaultPort string, scan pct.ByteScanner) (*Host, error) {
	if scan != nil {
		scan(len(raw))
	}
	if raw == "" {
		return nil, kindErr(KindEmptyHost, 0)
	}
	h := &Host{}
	hostText, portText, err := split(raw)
	if err != nil {
		return nil, err
	}
	if len(hostText) > 0 && hostText[0] == '[' {
		h.ip6 = true
		norm, perr := normalizeIPv6(hostText)
		if perr != nil {
			return nil, perr
		}
		h.name = norm
	} else {
		h.name = normalizeRegName(hostText)
		if h.name == "" {
			return nil, kindErr(KindEmptyHost, 0)
		}
	}
	port, perr := normalizePort(portText, defaultPort)
	if perr != nil {
		return nil, perr
	}
	h.port = port
	return h, nil
}

// split separates host from the trailing ":port", honoring IPv6 brackets.
func split(raw string) (string, string, error) {
	if raw[0] == '[' {
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			return "", "", kindErr(KindBadBracket, 0)
		}
		rest := raw[end+1:]
		if rest == "" {
			return raw[:end+1], "", nil
		}
		if rest[0] != ':' {
			return "", "", kindErr(KindBadBracket, end+1)
		}
		return raw[:end+1], rest[1:], nil
	}
	if strings.IndexByte(raw, '[') >= 0 {
		return "", "", kindErr(KindBadBracket, strings.IndexByte(raw, '['))
	}
	if colon := strings.IndexByte(raw, ':'); colon >= 0 {
		if strings.LastIndexByte(raw, ':') != colon {
			return "", "", kindErr(KindBadBracket, colon)
		}
		return raw[:colon], raw[colon+1:], nil
	}
	return raw, "", nil
}

func normalizeRegName(s string) string {
	b := []byte(strings.ToLower(s))
	for len(b) > 1 && b[len(b)-1] == '.' {
		b = b[:len(b)-1]
	}
	return string(b)
}

func normalizePort(raw, defaultPort string) (string, error) {
	if raw == "" {
		return "", nil // empty "host:" is the same as no port
	}
	for _, c := range []byte(raw) {
		if c < '0' || c > '9' {
			return "", kindErr(KindBadPort, 0)
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n > 65535 {
		return "", kindErr(KindBadPort, 0)
	}
	p := strconv.Itoa(n) // strips leading zeros
	if p == defaultPort {
		return "", nil
	}
	return p, nil
}

func (h *Host) String() string {
	if h.ip6 {
		return "[" + h.name + "]"
	}
	return h.name
}

// Authority renders host with a non-empty port.
func (h *Host) Authority() string {
	if h.port == "" {
		return h.String()
	}
	return h.String() + ":" + h.port
}

// Port returns the normalized port ("" when absent).
func (h *Host) Port() string { return h.port }

// IsIPv6 reports whether the host is an IPv6 literal.
func (h *Host) IsIPv6() bool { return h.ip6 }

// Name returns the host name without IPv6 brackets.
func (h *Host) Name() string { return h.name }
