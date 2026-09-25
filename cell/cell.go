package cell

// Cell describes one source field. End is the exclusive source byte offset.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal reports exact value and quoting equality.
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && bytesEqual(c.Value, o.Value)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
