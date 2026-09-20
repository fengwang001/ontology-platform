package pctenc

const hexUpper = "0123456789ABCDEF"

// Encode percent-encodes s according to the safe set of mode m.
//
// Unreserved characters and the mode-specific safe characters are
// copied verbatim; every other byte becomes "%XX" with uppercase hex.
// Multi-byte UTF-8 sequences are therefore encoded one byte at a time.
// If s needs no encoding it is returned unchanged.
func Encode(s string, m Mode) string {
	table := encodeTable(m)
	for i := 0; i < len(s); i++ {
		if !table[s[i]] {
			return encodeSlow(s, table, i)
		}
	}
	return s
}

func encodeSlow(s string, table [256]bool, first int) string {
	needed := len(s)
	for i := first; i < len(s); i++ {
		if !table[s[i]] {
			needed += 2
		}
	}

	out := make([]byte, needed)
	copy(out, s[:first])
	n := first
	for i := first; i < len(s); i++ {
		b := s[i]
		if table[b] {
			out[n] = b
			n++
			continue
		}
		out[n] = '%'
		out[n+1] = hexUpper[b>>4]
		out[n+2] = hexUpper[b&0x0f]
		n += 3
	}
	return string(out)
}

func encodeTable(m Mode) [256]bool {
	if m >= 0 && m <= Fragment {
		return safe[m]
	}
	return safe[Query]
}
