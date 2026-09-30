package norm

// RawTail returns undecided raw original bytes kept verbatim at Close.
func (n *Normalizer) RawTail() int { return n.tailRaw }

// Cell is one byte of fragment output with enough metadata for joining.
type Cell struct {
	Byte byte
	Orig int // global-independent: local original offset of the source byte
	Kind uint8
}

const (
	CellLit = 0 // verbatim literal
	CellNL  = 1 // already-decided newline; Orig points at its \n
	CellRaw = 2 // undecided raw byte at fragment tail (\r or space/tab)
)

// Cells returns annotated output for fragment-mode joining.
func (n *Normalizer) Cells() []Cell {
	var cs []Cell
	for _, s := range n.mp.Segs() {
		for k := 0; k < s.OLen; k++ {
			c := Cell{Orig: s.I + k, Byte: n.out[s.O+k], Kind: CellLit}
			if c.Byte == '\n' {
				c.Kind = CellNL
			}
			cs = append(cs, c)
		}
	}
	if n.tailRaw > 0 {
		from := n.LenOrig() - n.tailRaw
		for i := len(cs) - 1; i >= 0; i-- {
			if cs[i].Orig < from {
				break
			}
			cs[i].Kind = CellRaw
		}
	}
	return cs
}

// LenOrig returns consumed original bytes.
func (n *Normalizer) LenOrig() int { return n.mp.LenOrig() }
