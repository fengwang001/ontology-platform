package host

import "strconv"

// Authority is the normalized authority of a URL.
type Authority struct {
	Host    string // canonical host text: lowercase reg-name, or "[v6]"
	Port    int    // normalized port, 0 means no explicit port
	HasPort bool   // whether a port separator with digits was present
	IsIPv6  bool
}

// Result is the normalization outcome.
type Result struct {
	Auth    Authority
	Changed bool
	Scanned int
}

// Normalize canonicalizes an authority ("host", "host:port",
// "[v6]:port"). Trailing dots on reg-names are removed (DNS root label is
// insignificant), the host is ASCII-lowercased, default ports are kept here
// (callers know the scheme) and empty/leading-zero ports are normalized.
func Normalize(authority string) (Result, error) {
	return NormalizeWithDefault(authority, 0)
}

// NormalizeWithDefault drops an explicit port equal to defaultPort
// (0 disables dropping). It returns detailed rewrite flags via resultAtoms.
func NormalizeWithDefault(authority string, defaultPort int) (Result, error) {
	orig := authority
	if authority == "" {
		return Result{}, &Error{Kind: KindEmpty, Offset: 0}
	}
	var hostPart, portPart string
	var hasPort bool
	if authority[0] == '[' {
		closeB := indexByte(authority, ']')
		if closeB < 0 {
			return Result{}, &Error{Kind: KindBracket, Offset: 0, Detail: "missing ]"}
		}
		if indexByte(authority[closeB+1:], ']') >= 0 || indexByte(authority[1:closeB], '[') >= 0 {
			return Result{}, &Error{Kind: KindBracket, Offset: 0, Detail: "stray brackets"}
		}
		groups, err := parseIPv6(authority[1:closeB])
		if err != nil {
			return Result{}, err
		}
		canonical := "[" + formatIPv6(groups) + "]"
		hostPart = canonical
		rest := authority[closeB+1:]
		if rest != "" {
			if rest[0] != ':' {
				return Result{}, &Error{Kind: KindBracket, Offset: closeB + 1, Detail: "text after ]"}
			}
			portPart = rest[1:]
			hasPort = true
		}
	} else {
		if indexByte(authority, '[') >= 0 || indexByte(authority, ']') >= 0 {
			return Result{}, &Error{Kind: KindBracket, Offset: 0, Detail: "stray bracket"}
		}
		colon := lastIndexByte(authority, ':')
		if colon >= 0 {
			hostPart, portPart, hasPort = authority[:colon], authority[colon+1:], true
		} else {
			hostPart = authority
		}
	}
	if hostPart == "" || hostPart == "[]" {
		return Result{}, &Error{Kind: KindEmpty, Offset: 0}
	}
	if authority[0] != '[' {
		trimmed := hostPart
		for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '.' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		lower := asciiLower(trimmed)
		hostPart = lower
	}
	port := 0
	if hasPort {
		if portPart == "" {
			hasPort = false // "host:" is the same as no port
		} else {
			p, err := parsePort(portPart)
			if err != nil {
				return Result{}, err
			}
			port = p
			if defaultPort != 0 && p == defaultPort {
				hasPort = false
				port = 0
			}
		}
	}
	canonical := hostPart
	if hasPort {
		canonical = hostPart + ":" + strconv.Itoa(port)
	}
	res := Result{
		Auth:    Authority{Host: hostPart, Port: port, HasPort: hasPort, IsIPv6: authority[0] == '['},
		Changed: canonical != orig,
		Scanned: len(orig),
	}
	return res, nil
}

func parsePort(s string) (int, error) {
	if len(s) == 0 || len(s) > 5 {
		return 0, &Error{Kind: KindPort, Detail: "bad length"}
	}
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &Error{Kind: KindPort, Offset: i, Detail: "non-digit port"}
		}
		v = v*10 + int(c-'0')
	}
	if v > 65535 {
		return 0, &Error{Kind: KindPort, Detail: "port > 65535"}
	}
	return v, nil
}

func asciiLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
