package pctenc

// safeTable is a 256-bit bitmap: bit b is set when byte b may be
// emitted literally (never percent-encoded) in the given mode.
type safeTable [4]uint64

func (t *safeTable) set(b byte) {
	t[b>>6] |= 1 << (b & 63)
}

func (t *safeTable) has(b byte) bool {
	return t[b>>6]&(1<<(b&63)) != 0
}

// unreserved holds RFC 3986 unreserved characters:
// A-Z a-z 0-9 '-' '_' '.' '~'. These are safe in every mode.
var unreserved = func() (t safeTable) {
	for b := byte('A'); b <= 'Z'; b++ {
		t.set(b)
	}
	for b := byte('a'); b <= 'z'; b++ {
		t.set(b)
	}
	for b := byte('0'); b <= '9'; b++ {
		t.set(b)
	}
	for _, b := range []byte{'-', '_', '.', '~'} {
		t.set(b)
	}
	return t
}()

// safeFor returns the safe-byte table for mode m.
// Unknown modes fall back to the strictest set (unreserved only).
func safeFor(m Mode) safeTable {
	t := unreserved
	switch m {
	case Path:
		// Path segments additionally allow these sub-delims and
		// gen-delims to appear literally.
		for _, b := range []byte{':', '@', '&', '=', '+', '$', ','} {
			t.set(b)
		}
	case Query:
		// Query keys/values encode everything outside unreserved;
		// in particular '&', '=', '+' and '#' must be escaped.
	case Fragment:
		for _, b := range []byte{'/', '?'} {
			t.set(b)
		}
	}
	return t
}
