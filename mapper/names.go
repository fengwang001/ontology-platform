package mapper

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const hexUpper = "0123456789ABCDEF"

// validSrc reports whether src is a legal source name: non-empty, valid
// UTF-8, and free of '/' and NUL.
func validSrc(src string) bool {
	if src == "" {
		return false
	}
	if strings.IndexByte(src, '/') >= 0 || strings.IndexByte(src, 0) >= 0 {
		return false
	}
	if strings.ToValidUTF8(src, "\x00") != src {
		return false
	}
	return true
}

// fold lower-cases ASCII A-Z only.
func fold(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// fnv1a32 returns the 32-bit FNV-1a hash of data.
func fnv1a32(data []byte) uint32 {
	const offset = uint32(2166136261)
	const prime = uint32(16777619)
	h := offset
	for _, c := range data {
		h ^= uint32(c)
		h *= prime
	}
	return h
}

func escapeByte(b byte) string {
	return "%" + string(hexUpper[b>>4]) + string(hexUpper[b&0xF])
}

func mustEscape(b byte) bool {
	switch b {
	case '<', '>', ':', '"', '\\', '|', '?', '*', '%':
		return true
	}
	return b < 0x20
}

// tokens splits a mapped name (containing only raw runes or %XX escapes)
// into tokens of one rune or three bytes ("%" + two hex digits).
func tokens(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			out = append(out, s[i:i+3])
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		out = append(out, string(r))
		i += size
	}
	return out
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f'
}

// baseMap applies the four-step base mapping. It assumes src is valid.
func baseMap(src string, maxBytes int) string {
	// Step 1: per-rune escaping.
	var b strings.Builder
	b.Grow(len(src))
	for _, r := range src {
		if r < 0x80 && mustEscape(byte(r)) {
			b.WriteString(escapeByte(byte(r)))
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()

	// Step 2: trailing '.' or space.
	if len(s) > 0 {
		switch s[len(s)-1] {
		case '.':
			s = s[:len(s)-1] + "%2E"
		case ' ':
			s = s[:len(s)-1] + "%20"
		}
	}

	// Step 3: reserved device stem.
	stem := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		stem = s[:i]
	}
	if isReserved(fold(stem)) {
		_, size := utf8.DecodeRuneInString(s)
		s = escapeByte(s[0]) + s[size:]
	}

	// Step 4: truncation at token boundaries with FNV-1a hash.
	if len(s) > maxBytes {
		h := fnv1a32([]byte(s))
		hs := fmt.Sprintf("~%08X", h)
		toks := tokens(s)
		pre := 0
		for _, tk := range toks {
			if pre+len(tk)+len(hs) > maxBytes {
				break
			}
			pre += len(tk)
		}
		s = s[:pre] + hs
	}
	return s
}

// splitBaseExt splits t0 at the last '.' with index > 0.
func splitBaseExt(t0 string) (base, ext string) {
	i := strings.LastIndexByte(t0, '.')
	if i > 0 {
		return t0[:i], t0[i:]
	}
	return t0, ""
}

var reservedStems = func() map[string]bool {
	m := map[string]bool{
		"con": true, "prn": true, "aux": true, "nul": true,
	}
	for c := '1'; c <= '9'; c++ {
		m["com"+string(c)] = true
		m["lpt"+string(c)] = true
	}
	return m
}()

func isReserved(stem string) bool { return reservedStems[stem] }

// uniqueName allocates the first collision-free name starting from t0
// inside directory d.
func uniqueName(d *dentry, t0 string, maxBytes int) (string, error) {
	if _, taken := d.used[fold(t0)]; !taken {
		return t0, nil
	}
	base0, ext := splitBaseExt(t0)
	baseToks := tokens(base0)
	for n := 2; ; n++ {
		suffix := "~" + fmt.Sprintf("%d", n)
		// Shorten base token by token until the candidate fits.
		base := base0
		for len(base)+len(suffix)+len(ext) > maxBytes {
			if len(baseToks) == 0 {
				return "", ErrCannotFit
			}
			baseToks = baseToks[:len(baseToks)-1]
			base = strings.Join(baseToks, "")
		}
		cand := base + suffix + ext
		if len(cand) > maxBytes {
			return "", ErrCannotFit
		}
		if _, taken := d.used[fold(cand)]; !taken {
			return cand, nil
		}
	}
}

// sortedNames returns d's mapped names in byte order.
func sortedNames(d *dentry) []string {
	out := make([]string, 0, len(d.used))
	for _, name := range d.used {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
