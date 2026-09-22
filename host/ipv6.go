package host

import (
	"strconv"
	"strings"
)

func hexVal(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// parseIPv6 parses the content inside brackets into 8 16-bit groups,
// expanding a single "::" compression marker.
func parseIPv6(s string) (groups [8]uint16, err error) {
	if strings.Contains(s, ":::") || strings.Count(s, "::") > 1 {
		return groups, &Error{Kind: KindIPv6, Detail: "bad or multiple ::"}
	}
	var hParts, tParts []string
	compressed := false
	if i := strings.Index(s, "::"); i >= 0 {
		compressed = true
		if s[:i] != "" {
			hParts = strings.Split(s[:i], ":")
		}
		if s[i+2:] != "" {
			tParts = strings.Split(s[i+2:], ":")
		}
	} else {
		hParts = strings.Split(s, ":")
	}
	if len(hParts)+len(tParts) > 8 {
		return groups, &Error{Kind: KindIPv6, Detail: "too many groups"}
	}
	if !compressed && len(hParts) != 8 {
		return groups, &Error{Kind: KindIPv6, Detail: "need exactly 8 groups or ::"}
	}
	parsePart := func(p string) (uint16, error) {
		if len(p) == 0 || len(p) > 4 {
			return 0, &Error{Kind: KindIPv6, Detail: "bad group"}
		}
		v := 0
		for i := 0; i < len(p); i++ {
			x, ok := hexVal(p[i])
			if !ok {
				return 0, &Error{Kind: KindIPv6, Detail: "non-hex group"}
			}
			v = v<<4 | x
		}
		return uint16(v), nil
	}
	var all [8]uint16
	idx := 0
	for _, p := range hParts {
		v, perr := parsePart(p)
		if perr != nil {
			return groups, perr
		}
		all[idx] = v
		idx++
	}
	if compressed {
		idx += 8 - len(hParts) - len(tParts)
	}
	for _, p := range tParts {
		v, perr := parsePart(p)
		if perr != nil {
			return groups, perr
		}
		all[idx] = v
		idx++
	}
	if idx != 8 {
		return groups, &Error{Kind: KindIPv6, Detail: "short address"}
	}
	return all, nil
}

// formatIPv6 renders the canonical compressed form: lowercase hex, no
// leading zeros, and the longest run of at least two zero groups
// compressed with "::"; ties choose the leftmost run (RFC 5952).
func formatIPv6(g [8]uint16) string {
	bestStart, bestLen := -1, 1
	curStart, curLen := -1, 0
	for i := 0; i <= 8; i++ {
		zero := i < 8 && g[i] == 0
		if zero {
			if curLen == 0 {
				curStart = i
			}
			curLen++
			continue
		}
		if curLen > bestLen {
			bestStart, bestLen = curStart, curLen
		}
		curLen = 0
	}
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = strconv.FormatUint(uint64(g[i]), 16)
	}
	var segs []string
	for i := 0; i < 8; {
		if i == bestStart {
			segs = append(segs, "")
			if i == 0 {
				segs = append(segs, "")
			}
			i += bestLen
			continue
		}
		segs = append(segs, parts[i])
		i++
	}
	if bestStart+bestLen == 8 {
		segs = append(segs, "")
	}
	return strings.Join(segs, ":")
}
