package host

import (
	"strconv"
	"strings"
)

// normalizeIPv6 canonicalizes a bracketed IPv6 literal per RFC 5952:
// lowercase hex, no leading zeros, and the longest all-zero run compressed
// with "::" (ties: the first run wins).
func normalizeIPv6(lit string) (string, error) {
	if len(lit) < 2 || lit[0] != '[' || lit[len(lit)-1] != ']' {
		return "", kindErr(KindBadBracket, 0)
	}
	s := lit[1 : len(lit)-1]
	if s == "" {
		return "", kindErr(KindBadIPv6, 1)
	}
	parts, err := splitIPv6(s)
	if err != nil {
		return "", err
	}
	groups := make([]uint16, 0, 8)
	for _, p := range parts {
		v, err := parseGroup(p)
		if err != nil {
			return "", err
		}
		groups = append(groups, v)
	}
	if len(groups) != 8 {
		return "", kindErr(KindBadIPv6, 0)
	}

	start, bestLen := -1, 1
	i := 0
	for i < 8 {
		if groups[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && groups[j] == 0 {
			j++
		}
		if j-i > bestLen {
			start, bestLen = i, j-i
		}
		i = j
	}

	hexes := make([]string, 8)
	for k, g := range groups {
		hexes[k] = strconv.FormatUint(uint64(g), 16)
	}
	if start < 0 {
		return strings.Join(hexes, ":"), nil
	}
	end := start + bestLen
	seg := append([]string{}, hexes[:start]...)
	if start == 0 {
		seg = append(seg, "") // leading "::" needs an extra empty field
	}
	seg = append(seg, "") // the compressed all-zero run renders as "::"
	seg = append(seg, hexes[end:]...)
	if end == 8 {
		seg = append(seg, "") // trailing "::" needs an extra empty field
	}
	return strings.Join(seg, ":"), nil
}

func splitIPv6(s string) ([]string, error) {
	dc := -1
	for i := 0; i < len(s)-1; i++ {
		if s[i] == ':' && s[i+1] == ':' {
			if dc >= 0 {
				return nil, kindErr(KindBadIPv6, i+1)
			}
			dc = i
		}
	}
	if dc < 0 {
		p := splitColon(s)
		if len(p) != 8 {
			return nil, kindErr(KindBadIPv6, 0)
		}
		return p, nil
	}
	var left, right []string
	if dc > 0 {
		left = splitColon(s[:dc])
	}
	if dc+2 < len(s) {
		right = splitColon(s[dc+2:])
	}
	if len(left)+len(right) >= 8 {
		return nil, kindErr(KindBadIPv6, dc)
	}
	missing := 8 - len(left) - len(right)
	p := append([]string{}, left...)
	for k := 0; k < missing; k++ {
		p = append(p, "0")
	}
	p = append(p, right...)
	return p, nil
}

func splitColon(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func parseGroup(g string) (uint16, error) {
	if len(g) == 0 || len(g) > 4 {
		return 0, kindErr(KindBadIPv6, 0)
	}
	v := 0
	for _, c := range []byte(g) {
		d, ok := hexDigit(c)
		if !ok {
			return 0, kindErr(KindBadIPv6, 0)
		}
		v = v<<4 | d
	}
	return uint16(v), nil
}

func hexDigit(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	default:
		return 0, false
	}
}
