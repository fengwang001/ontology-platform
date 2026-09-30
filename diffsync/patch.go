package diffsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
)

// Distinguishable failure reasons for patch application. Use errors.Is to
// test for them.
var (
	// ErrMalformedPatch: bad magic, unknown opcode or trailing garbage.
	ErrMalformedPatch = errors.New("diffsync: malformed patch")
	// ErrTruncatedPatch: the patch byte stream ends prematurely.
	ErrTruncatedPatch = errors.New("diffsync: truncated patch")
	// ErrOldChecksumMismatch: the patch was made for a different old file.
	ErrOldChecksumMismatch = errors.New("diffsync: old file checksum mismatch")
	// ErrInstructionOutOfRange: a copy instruction references a
	// non-existent block of the old file.
	ErrInstructionOutOfRange = errors.New("diffsync: instruction out of range")
	// ErrResultChecksumMismatch: the reconstructed content does not match
	// the checksum declared by the patch.
	ErrResultChecksumMismatch = errors.New("diffsync: result checksum mismatch")
)

// patchMagic identifies the binary patch format (version 1).
const patchMagic = "ODP1"

// Patch layout, all integers big-endian:
//
//	magic        4 bytes  "ODP1"
//	blockSize    uint32
//	oldLength    uint64
//	oldChecksum  32 bytes (SHA-256 of the old file)
//	newLength    uint64
//	newChecksum  32 bytes (SHA-256 of the expected result)
//	instrCount   uint32
//	instructions instrCount x:
//	  copy:    0x01, blockIndex uint32
//	  literal: 0x02, length uint32, data[length]

// MarshalPatch serializes d into its deterministic binary form.
func MarshalPatch(d *Delta) ([]byte, error) {
	if d == nil || d.BlockSize <= 0 {
		return nil, ErrInvalidBlockSize
	}
	if d.BlockSize > math.MaxUint32 || d.OldLength < 0 || d.NewLength < 0 ||
		len(d.Instructions) > math.MaxUint32 {
		return nil, ErrMalformedPatch
	}
	buf := new(bytes.Buffer)
	buf.WriteString(patchMagic)
	writeU32(buf, uint32(d.BlockSize))
	writeU64(buf, uint64(d.OldLength))
	buf.Write(d.OldChecksum[:])
	writeU64(buf, uint64(d.NewLength))
	buf.Write(d.NewChecksum[:])
	writeU32(buf, uint32(len(d.Instructions)))
	for _, ins := range d.Instructions {
		switch ins.Op {
		case OpCopy:
			buf.WriteByte(byte(OpCopy))
			writeU32(buf, ins.Index)
		case OpLiteral:
			if len(ins.Data) == 0 || len(ins.Data) > math.MaxUint32 {
				return nil, ErrMalformedPatch
			}
			buf.WriteByte(byte(OpLiteral))
			writeU32(buf, uint32(len(ins.Data)))
			buf.Write(ins.Data)
		default:
			return nil, ErrMalformedPatch
		}
	}
	return buf.Bytes(), nil
}

// ApplyPatch validates patch against oldData and returns the reconstructed
// new content. On any error it returns nil and writes nothing.
func ApplyPatch(oldData, patch []byte) ([]byte, error) {
	p := &parser{data: patch}

	magic, err := p.take(len(patchMagic))
	if err != nil {
		return nil, err
	}
	if string(magic) != patchMagic {
		return nil, ErrMalformedPatch
	}
	blockSize, err := p.u32()
	if err != nil {
		return nil, err
	}
	if blockSize == 0 {
		return nil, ErrInvalidBlockSize
	}
	oldLength, err := p.u64()
	if err != nil {
		return nil, err
	}
	oldChecksum, err := p.take(StrongChecksumSize)
	if err != nil {
		return nil, err
	}
	newLength, err := p.u64()
	if err != nil {
		return nil, err
	}
	newChecksum, err := p.take(StrongChecksumSize)
	if err != nil {
		return nil, err
	}

	// The patch must have been generated for exactly this old file.
	oldSum := sha256.Sum256(oldData)
	if uint64(len(oldData)) != oldLength || !bytes.Equal(oldSum[:], oldChecksum) {
		return nil, ErrOldChecksumMismatch
	}

	instrCount, err := p.u32()
	if err != nil {
		return nil, err
	}
	type parsedInstr struct {
		op    OpCode
		index uint32
		data  []byte
	}
	instrs := make([]parsedInstr, 0, min(int(instrCount), 1<<20))
	for k := uint32(0); k < instrCount; k++ {
		opByte, err := p.take(1)
		if err != nil {
			return nil, err
		}
		switch OpCode(opByte[0]) {
		case OpCopy:
			index, err := p.u32()
			if err != nil {
				return nil, err
			}
			instrs = append(instrs, parsedInstr{op: OpCopy, index: index})
		case OpLiteral:
			length, err := p.u32()
			if err != nil {
				return nil, err
			}
			data, err := p.take(int(length))
			if err != nil {
				return nil, err
			}
			instrs = append(instrs, parsedInstr{op: OpLiteral, data: data})
		default:
			return nil, ErrMalformedPatch
		}
	}
	if p.pos != len(p.data) {
		return nil, ErrMalformedPatch
	}

	var numBlocks uint64
	if oldLength > 0 {
		numBlocks = (oldLength + uint64(blockSize) - 1) / uint64(blockSize)
	}

	// Reconstruct into a fresh buffer; nothing is written anywhere until
	// every validation below has passed.
	out := make([]byte, 0, min(newLength, 1<<30))
	for _, ins := range instrs {
		switch ins.op {
		case OpCopy:
			if uint64(ins.index) >= numBlocks {
				return nil, ErrInstructionOutOfRange
			}
			start := uint64(ins.index) * uint64(blockSize)
			end := min(start+uint64(blockSize), oldLength)
			out = append(out, oldData[start:end]...)
		case OpLiteral:
			out = append(out, ins.data...)
		}
		if uint64(len(out)) > newLength {
			return nil, ErrResultChecksumMismatch
		}
	}

	outSum := sha256.Sum256(out)
	if uint64(len(out)) != newLength || !bytes.Equal(outSum[:], newChecksum) {
		return nil, ErrResultChecksumMismatch
	}
	return out, nil
}

// ApplyPatchToFile applies patch to the file at oldPath and atomically
// writes the result to dstPath (temp file + rename in the same directory).
// On any error dstPath is left byte-for-byte untouched.
func ApplyPatchToFile(oldPath string, patch []byte, dstPath string) error {
	oldData, err := os.ReadFile(oldPath)
	if err != nil {
		return err
	}
	result, err := ApplyPatch(oldData, patch)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".diffsync-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has happened

	if _, err := tmp.Write(result); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dstPath)
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

// parser is a strict big-endian cursor over the patch byte stream; every
// short read is reported as ErrTruncatedPatch.
type parser struct {
	data []byte
	pos  int
}

func (p *parser) take(n int) ([]byte, error) {
	if n < 0 || len(p.data)-p.pos < n {
		return nil, ErrTruncatedPatch
	}
	b := p.data[p.pos : p.pos+n]
	p.pos += n
	return b, nil
}

func (p *parser) u32() (uint32, error) {
	b, err := p.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (p *parser) u64() (uint64, error) {
	b, err := p.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}
