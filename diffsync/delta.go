package diffsync

import "crypto/sha256"

// OpCode identifies the kind of a patch instruction.
type OpCode byte

const (
	// OpCopy reuses block Index of the old file.
	OpCopy OpCode = 1
	// OpLiteral emits raw bytes.
	OpLiteral OpCode = 2
)

// Instruction is one patch operation: either "copy old block Index" or
// "emit these literal bytes". Adjacent literals are merged into one
// instruction.
type Instruction struct {
	Op    OpCode
	Index uint32
	Data  []byte
}

// DeltaStats reports matching statistics gathered while generating a
// delta. StrongChecks never exceeds WeakMatches.
type DeltaStats struct {
	WeakMatches  int
	StrongChecks int
}

// Delta is a generated patch plus the metadata needed to validate its
// application. Use MarshalPatch to serialize it deterministically.
type Delta struct {
	BlockSize    int
	OldLength    int64
	OldChecksum  [StrongChecksumSize]byte
	NewLength    int64
	NewChecksum  [StrongChecksumSize]byte
	Instructions []Instruction
	Stats        DeltaStats
}

// LiteralBytes returns the total number of literal bytes in the delta.
func (d *Delta) LiteralBytes() int {
	total := 0
	for _, ins := range d.Instructions {
		if ins.Op == OpLiteral {
			total += len(ins.Data)
		}
	}
	return total
}

// GenerateDelta scans newData against sig and emits copy/literal
// instructions. It only reads sig, so one signature may serve many
// concurrent senders. The same inputs always produce the same delta.
func GenerateDelta(sig *Signature, newData []byte) (*Delta, error) {
	if sig == nil || sig.BlockSize <= 0 {
		return nil, ErrInvalidBlockSize
	}
	d := &Delta{
		BlockSize:   sig.BlockSize,
		OldLength:   sig.FileLength,
		OldChecksum: sig.FileChecksum,
		NewLength:   int64(len(newData)),
		NewChecksum: sha256.Sum256(newData),
	}

	blockSize := sig.BlockSize
	var literal []byte
	flushLiteral := func() {
		if len(literal) > 0 {
			d.Instructions = append(d.Instructions, Instruction{Op: OpLiteral, Data: literal})
			literal = nil
		}
	}

	var rollA, rollB uint32
	rolling := false
	for i := 0; i < len(newData); {
		n := blockSize
		if rem := len(newData) - i; rem < n {
			n = rem
		}
		window := newData[i : i+n]

		var weak uint32
		if n == blockSize {
			if rolling {
				rollA, rollB, weak = rollWeak(rollA, rollB, blockSize, newData[i-1], newData[i+blockSize-1])
			} else {
				weak = weakChecksum(window)
				rollA, rollB = splitWeak(weak)
			}
			rolling = true
		} else {
			weak = weakChecksum(window)
			rolling = false
		}

		matched := false
		for _, idx := range sig.weakIndex[weak] {
			blk := sig.Blocks[idx]
			d.Stats.WeakMatches++
			// A short last block may only be reused for an equally
			// long segment, and vice versa.
			if blk.Length != n {
				continue
			}
			d.Stats.StrongChecks++
			if blk.Strong == sha256.Sum256(window) {
				flushLiteral()
				d.Instructions = append(d.Instructions, Instruction{Op: OpCopy, Index: uint32(idx)})
				i += n
				rolling = false
				matched = true
				break
			}
		}
		if !matched {
			literal = append(literal, newData[i])
			i++
		}
	}
	flushLiteral()
	return d, nil
}
