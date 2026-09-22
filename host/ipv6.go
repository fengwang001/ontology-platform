package host

import (
	"strconv"
	"strings"
)

// normalizeIPv6 parses an RFC 4291 IPv6 literal without brackets and returns
// its compressed lowercase form (longest run of zero groups replaced by "::").
func normalizeIPv6(s string) (string, error) {
	if s == "" {
		return "", ErrBadIPv6
	}
	head, tail, hadCompress, err := splitCompression(s)
	if err != nil {
		return "", err
	}
	left := parseGroups(head)
	right := parseGroups(tail)
	if left == nil || right == nil {
		return "", ErrBadIPv6
	}
	if !hadCompress && len(left)+len(right) != 8 {
		return "", ErrBadIPv6
	}
	if hadCompress {
		// "::" alone is valid (all zeros); each side contributes <= 7.
		if len(left)+len(right) > 7 {
			return "", ErrBadIPv6
		}
	}
	groups := make([]int, 0, 8)
	groups = append(groups, left...)
	for len(groups)+len(right) < 8 {
		groups = append(groups, 0)
	}
	groups = append(groups, right...)

	// Find longest zero run of length >= 2.
	bestStart, bestLen := -1, 0
	for i := 0; i < 8; {
		if groups[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && groups[j] == 0 {
			j++
		}
		if j-i > bestLen {
			bestStart, bestLen = i, j-i
		}
		i = j
	}

	parts := make([]string, 8)
	for i, g := range groups {
		parts[i] = strconv.FormatInt(int64(g), 16)
	}
	if bestLen < 2 {
		return strings.Join(parts, ":"), nil
	}
	var b strings.Builder
	for i := 0; i < bestStart; i++ {
		b.WriteString(parts[i])
		b.WriteByte(':')
	}
	b.WriteString("::")
	for i := bestStart + bestLen; i < 8; i++ {
		if i > bestStart+bestLen {
			b.WriteByte(':')
		}
		b.WriteString(parts[i])
	}
	return b.String(), nil
}

func splitCompression(s string) (left, right string, had bool, err error) {
	i := strings.Index(s, "::")
	if i < 0 {
		return s, "", false, nil
	}
	if strings.Index(s[i+2:], "::") >= 0 {
		return "", "", false, ErrBadIPv6
	}
	return s[:i], s[i+2:], true, nil
}

func parseGroups(s string) []int {
	if s == "" {
		return []int{}
	}
	fields := strings.Split(s, ":")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if len(f) == 0 || len(f) > 4 {
			return nil
		}
		v, err := strconv.ParseUint(f, 16, 16)
		if err != nil {
			return nil
		}
		out = append(out, int(v))
	}
	return out
}
