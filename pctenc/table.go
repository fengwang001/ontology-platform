package pctenc

// safeTable is a 256-bit bitmap: bit b is set when byte b may be emitted
// literally (never percent-encoded) in the corresponding mode.
type safeTable [4]uint64

func (t *safeTable) set(bs string) {
	for i := 0; i < len(bs); i++ {
		b := bs[i]
		t[b>>6] |= 1 << (b & 63)
	}
}

func (t *safeTable) has(b byte) bool {
	return t[b>>6]&(1<<(b&63)) != 0
}

// unreserved per RFC 3986: never encoded in any mode.
const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"abcdefghijklmnopqrstuvwxyz" +
	"0123456789" +
	"-_.~"

var safeByMode = [3]safeTable{
	Path:     makeSafe(unreserved + ":@&=+$,"),
	Query:    makeSafe(unreserved),
	Fragment: makeSafe(unreserved + "/?"),
}

func makeSafe(chars string) safeTable {
	var t safeTable
	t.set(chars)
	return t
}

func tableFor(m Mode) *safeTable {
	if m < Path || m > Fragment {
		m = Path
	}
	return &safeByMode[m]
}
