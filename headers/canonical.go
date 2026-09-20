package headers

// canonical returns the canonical header-field name: each dash-separated
// segment has its first letter upper-cased and the remaining letters
// lower-cased. Bytes that are not ASCII letters are left untouched.
func canonical(name string) string {
	b := []byte(name)
	upper := true
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '-' {
			upper = true
			continue
		}
		switch {
		case upper && c >= 'a' && c <= 'z':
			b[i] = c - ('a' - 'A')
		case !upper && c >= 'A' && c <= 'Z':
			b[i] = c + ('a' - 'A')
		}
		upper = false
	}
	return string(b)
}
