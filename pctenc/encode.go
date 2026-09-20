package pctenc

const upperHex = "0123456789ABCDEF"

// Encode percent-encodes s according to m. Bytes that are safe in m are
// left as-is; everything else is emitted as uppercase %XX, one escape per
// UTF-8 byte. If nothing needs escaping, s is returned unchanged.
func Encode(s string, m Mode) string {
	t := tableFor(m)
	need := 0
	for i := 0; i < len(s); i++ {
		if !t.has(s[i]) {
			need++
		}
	}
	if need == 0 {
		return s
	}
	buf := make([]byte, 0, len(s)+2*need)
	for i := 0; i < len(s); i++ {
		b := s[i]
		if t.has(b) {
			buf = append(buf, b)
		} else {
			buf = append(buf, '%', upperHex[b>>4], upperHex[b&15])
		}
	}
	return string(buf)
}
