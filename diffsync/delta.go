package diffsync

// Op is a patch instruction opcode.
type Op byte

const (
	// OpCopy reuses block Index of the old file.
	OpCopy Op = 0
	// OpLiteral emits raw bytes.
	OpLiteral Op = 1
)

// Instruction is a single patch instruction: either "reuse old block
// Index" or "emit these literal bytes".
type Instruction struct {
	Op    Op
	Index int    // valid when Op == OpCopy
	Data  []byte // valid when Op == OpLiteral
}

// Stats reports what the delta generator did. StrongChecks never exceeds
// WeakHits because a strong checksum is only computed after a weak hit.
type Stats struct {
	WeakHits     int // candidate (window, block) pairs passing the weak test
	StrongChecks int // strong checksum computations
	StrongHits   int // weak hits confirmed by the strong checksum
	CopyBlocks   int // OpCopy instructions emitted
	LiteralBytes int // literal bytes emitted
}

// GenerateDelta scans newData at every offset. A window whose weak
// checksum matches a signature block is reused only after the strong
// checksum confirms identical content; otherwise the current byte is
// emitted literally. Adjacent literals are merged. The result is
// deterministic: the same signature and input always produce the same
// patch bytes.
func GenerateDelta(sig *Signature, newData []byte) ([]byte, Stats, error) {
	var st Stats
	if sig == nil || sig.BlockSize <= 0 {
		return nil, st, ErrInvalidBlockSize
	}
	bs := sig.BlockSize
	var insns []Instruction
	var lit []byte // pending literal run

	flushLiteral := func() {
		if len(lit) > 0 {
			data := make([]byte, len(lit))
			copy(data, lit)
			insns = append(insns, Instruction{Op: OpLiteral, Data: data})
			lit = lit[:0]
		}
	}

	for off := 0; off < len(newData); {
		wlen := bs
		if rem := len(newData) - off; rem < wlen {
			wlen = rem
		}
		window := newData[off : off+wlen]
		w := weakSum(window)

		matched := -1
		for _, idx := range sig.byWeak[w] {
			blk := &sig.Blocks[idx]
			if blk.Len != wlen {
				// The short last block only matches an equally
				// short fragment; never a full-size window.
				continue
			}
			st.WeakHits++
			st.StrongChecks++
			if strongSum(window) == blk.Strong {
				st.StrongHits++
				matched = idx
				break
			}
		}

		if matched >= 0 {
			flushLiteral()
			insns = append(insns, Instruction{Op: OpCopy, Index: matched})
			st.CopyBlocks++
			off += wlen
		} else {
			lit = append(lit, newData[off])
			st.LiteralBytes++
			off++
		}
	}
	flushLiteral()

	p := &Patch{
		BlockSize: bs,
		OldLen:    sig.FileLen,
		OldStrong: sig.FileStrong,
		NewLen:    int64(len(newData)),
		NewStrong: strongSum(newData),
		Insns:     insns,
	}
	return p.Marshal(), st, nil
}
