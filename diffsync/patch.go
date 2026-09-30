package diffsync

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Distinguishable rejection reasons, all testable with errors.Is.
var (
	ErrBadMagic               = errors.New("diffsync: bad patch magic")
	ErrPatchTruncated         = errors.New("diffsync: patch truncated")
	ErrOldChecksumMismatch    = errors.New("diffsync: old file checksum mismatch")
	ErrInstructionOutOfRange  = errors.New("diffsync: instruction out of range")
	ErrResultChecksumMismatch = errors.New("diffsync: result checksum mismatch")
)

// patchMagic identifies the binary patch format (version 1).
var patchMagic = []byte("BDSYNC01")

// Patch describes how to rebuild the new file from the old file.
//
// Binary layout (all integers big-endian):
//
//	magic        [8]byte  "BDSYNC01"
//	blockSize    uint32
//	oldLen       uint64
//	oldStrong    [32]byte SHA-256 of the whole old file
//	newLen       uint64
//	newStrong    [32]byte SHA-256 of the whole new file
//	insnCount    uint32
//	insns        insnCount x { op uint8; OpCopy: index uint32 |
//	             OpLiteral: dataLen uint32, data [dataLen]byte }
type Patch struct {
	BlockSize int
	OldLen    int64
	OldStrong [StrongSize]byte
	NewLen    int64
	NewStrong [StrongSize]byte
	Insns     []Instruction
}

// Marshal encodes the patch deterministically.
func (p *Patch) Marshal() []byte {
	var buf bytes.Buffer
	buf.Write(patchMagic)
	writeU32(&buf, uint32(p.BlockSize))
	writeU64(&buf, uint64(p.OldLen))
	buf.Write(p.OldStrong[:])
	writeU64(&buf, uint64(p.NewLen))
	buf.Write(p.NewStrong[:])
	writeU32(&buf, uint32(len(p.Insns)))
	for _, in := range p.Insns {
		buf.WriteByte(byte(in.Op))
		switch in.Op {
		case OpCopy:
			writeU32(&buf, uint32(in.Index))
		case OpLiteral:
			writeU32(&buf, uint32(len(in.Data)))
			buf.Write(in.Data)
		}
	}
	return buf.Bytes()
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

// reader is a strict cursor over the patch bytes; every read past the
// end fails with ErrPatchTruncated.
type reader struct {
	data []byte
	off  int
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || len(r.data)-r.off < n {
		return nil, fmt.Errorf("%w at offset %d: need %d bytes, have %d",
			ErrPatchTruncated, r.off, n, len(r.data)-r.off)
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

// UnmarshalPatch decodes a patch, rejecting any truncation.
func UnmarshalPatch(data []byte) (*Patch, error) {
	r := &reader{data: data}
	magic, err := r.take(len(patchMagic))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(magic, patchMagic) {
		return nil, fmt.Errorf("%w: %q", ErrBadMagic, magic)
	}
	bs, err := r.u32()
	if err != nil {
		return nil, err
	}
	oldLen, err := r.u64()
	if err != nil {
		return nil, err
	}
	oldStrong, err := r.take(StrongSize)
	if err != nil {
		return nil, err
	}
	newLen, err := r.u64()
	if err != nil {
		return nil, err
	}
	newStrong, err := r.take(StrongSize)
	if err != nil {
		return nil, err
	}
	count, err := r.u32()
	if err != nil {
		return nil, err
	}
	p := &Patch{
		BlockSize: int(bs),
		OldLen:    int64(oldLen),
		NewLen:    int64(newLen),
	}
	copy(p.OldStrong[:], oldStrong)
	copy(p.NewStrong[:], newStrong)
	for i := uint32(0); i < count; i++ {
		opb, err := r.take(1)
		if err != nil {
			return nil, fmt.Errorf("instruction %d: %w", i, err)
		}
		switch Op(opb[0]) {
		case OpCopy:
			idx, err := r.u32()
			if err != nil {
				return nil, fmt.Errorf("instruction %d: %w", i, err)
			}
			p.Insns = append(p.Insns, Instruction{Op: OpCopy, Index: int(idx)})
		case OpLiteral:
			n, err := r.u32()
			if err != nil {
				return nil, fmt.Errorf("instruction %d: %w", i, err)
			}
			data, err := r.take(int(n))
			if err != nil {
				return nil, fmt.Errorf("instruction %d: %w", i, err)
			}
			cp := make([]byte, len(data))
			copy(cp, data)
			p.Insns = append(p.Insns, Instruction{Op: OpLiteral, Data: cp})
		default:
			return nil, fmt.Errorf("instruction %d: %w: unknown op %d",
				i, ErrInstructionOutOfRange, opb[0])
		}
	}
	if r.off != len(r.data) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrInstructionOutOfRange, len(r.data)-r.off)
	}
	return p, nil
}

// Apply rebuilds the new file from old and an encoded patch. It is pure:
// on any error nothing is written anywhere and the returned slice is nil.
// Rejection reasons are distinguishable via errors.Is:
//
//	ErrInvalidBlockSize        non-positive block size declared by the patch
//	ErrBadMagic                not a patch
//	ErrPatchTruncated          patch ends early
//	ErrOldChecksumMismatch     old file differs from what the patch declares
//	ErrInstructionOutOfRange   bad op, block index, or trailing garbage
//	ErrResultChecksumMismatch  rebuilt content differs from the declaration
func Apply(old, patchBytes []byte) ([]byte, error) {
	p, err := UnmarshalPatch(patchBytes)
	if err != nil {
		return nil, err
	}
	if p.BlockSize <= 0 {
		return nil, fmt.Errorf("%w, patch declares %d", ErrInvalidBlockSize, p.BlockSize)
	}
	if int64(len(old)) != p.OldLen || strongSum(old) != p.OldStrong {
		return nil, fmt.Errorf("%w: patch declares len=%d sha256=%x, target has len=%d sha256=%x",
			ErrOldChecksumMismatch, p.OldLen, p.OldStrong, len(old), strongSum(old))
	}
	nBlocks := NumBlocks(p.OldLen, p.BlockSize)
	var out bytes.Buffer
	if p.NewLen >= 0 && p.NewLen <= int64(len(old))+int64(len(patchBytes)) {
		out.Grow(int(p.NewLen))
	}
	for i, in := range p.Insns {
		switch in.Op {
		case OpCopy:
			if in.Index < 0 || in.Index >= nBlocks {
				return nil, fmt.Errorf("instruction %d: %w: block index %d, old file has %d blocks",
					i, ErrInstructionOutOfRange, in.Index, nBlocks)
			}
			start := int64(in.Index) * int64(p.BlockSize)
			end := start + int64(p.BlockSize)
			if end > p.OldLen {
				end = p.OldLen
			}
			out.Write(old[start:end])
		case OpLiteral:
			out.Write(in.Data)
		default:
			return nil, fmt.Errorf("instruction %d: %w: unknown op %d",
				i, ErrInstructionOutOfRange, byte(in.Op))
		}
	}
	result := out.Bytes()
	if int64(len(result)) != p.NewLen || strongSum(result) != p.NewStrong {
		return nil, fmt.Errorf("%w: patch declares len=%d sha256=%x, rebuilt len=%d sha256=%x",
			ErrResultChecksumMismatch, p.NewLen, p.NewStrong, len(result), strongSum(result))
	}
	return result, nil
}
