// Package patch 定义补丁格式，负责编解码与在目标端全有或全无地应用。
package patch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/chunk"
	"ontology/verify"
)

// Op 是补丁指令类型。
type Op byte

const (
	// OpReuse 复用目标端的第 Block 块。
	OpReuse Op = iota
	// OpData 写入一段新数据。
	OpData
)

// Instr 是一条补丁指令。
type Instr struct {
	Op    Op
	Block int    // OpReuse：目标端块号
	Data  []byte // OpData：新数据
}

// Patch 描述把目标端数据变为源端数据所需的全部指令。
type Patch struct {
	BlockSize int
	SrcLen    int
	SrcHash   [16]byte
	Instrs    []Instr
}

var magic = []byte("ONP1")

// headerLen = magic(4) | blockSize(4) | srcLen(8) | instrCount(4) | srcHash(16)
const headerLen = 4 + 4 + 8 + 4 + 16

// Encode 把补丁编码为字节流，尾部附带 CRC32。
func Encode(p Patch) []byte {
	out := make([]byte, 0, headerLen+4)
	out = append(out, magic...)
	out = appendU32(out, uint32(p.BlockSize))
	out = appendU64(out, uint64(p.SrcLen))
	out = appendU32(out, uint32(len(p.Instrs)))
	out = append(out, p.SrcHash[:]...)
	for _, in := range p.Instrs {
		out = append(out, byte(in.Op))
		if in.Op == OpReuse {
			out = appendU32(out, uint32(in.Block))
		} else {
			out = appendU32(out, uint32(len(in.Data)))
			out = append(out, in.Data...)
		}
	}
	return appendU32(out, crc32.ChecksumIEEE(out))
}

// Decode 解析补丁字节流，按失败位置返回可分类的截断错误。
func Decode(b []byte) (Patch, error) {
	if len(b) < headerLen || !bytes.Equal(b[:4], magic) {
		return Patch{}, verify.ErrHeaderIncomplete
	}
	p := Patch{
		BlockSize: int(binary.BigEndian.Uint32(b[4:8])),
		SrcLen:    int(binary.BigEndian.Uint64(b[8:16])),
	}
	copy(p.SrcHash[:], b[20:headerLen])
	n := int(binary.BigEndian.Uint32(b[16:20]))
	off := headerLen
	for k := 0; k < n; k++ {
		if off+1 > len(b) {
			return Patch{}, verify.ErrInstrIncomplete
		}
		op := Op(b[off])
		off++
		switch op {
		case OpReuse:
			if off+4 > len(b) {
				return Patch{}, verify.ErrInstrIncomplete
			}
			p.Instrs = append(p.Instrs, Instr{Op: op, Block: int(binary.BigEndian.Uint32(b[off : off+4]))})
			off += 4
		case OpData:
			if off+4 > len(b) {
				return Patch{}, verify.ErrInstrIncomplete
			}
			l := int(binary.BigEndian.Uint32(b[off : off+4]))
			off += 4
			if off+l > len(b) {
				return Patch{}, verify.ErrDataIncomplete
			}
			p.Instrs = append(p.Instrs, Instr{Op: op, Data: b[off : off+l]})
			off += l
		default:
			return Patch{}, verify.ErrInstrIncomplete
		}
	}
	if off+4 > len(b) || crc32.ChecksumIEEE(b[:off]) != binary.BigEndian.Uint32(b[off:off+4]) {
		return Patch{}, verify.ErrCRCMismatch
	}
	return p, nil
}

// Apply 校验整份补丁后在内存重建结果，全部通过才返回；失败则目标零改动。
func Apply(target, patchBytes []byte) ([]byte, error) {
	p, err := Decode(patchBytes)
	if err != nil {
		return nil, err
	}
	if p.BlockSize <= 0 {
		return nil, chunk.ErrBlockSize
	}
	numBlocks := (len(target) + p.BlockSize - 1) / p.BlockSize
	var out []byte
	for _, in := range p.Instrs {
		if in.Op == OpData {
			out = append(out, in.Data...)
			continue
		}
		if in.Block < 0 || in.Block >= numBlocks {
			return nil, fmt.Errorf("%w: block %d of %d", verify.ErrOutOfRange, in.Block, numBlocks)
		}
		start := in.Block * p.BlockSize
		end := start + p.BlockSize
		if end > len(target) {
			end = len(target)
		}
		out = append(out, target[start:end]...)
	}
	if len(out) != p.SrcLen {
		return nil, fmt.Errorf("%w: len %d != %d", verify.ErrMismatch, len(out), p.SrcLen)
	}
	if err := verify.CheckResult(p.SrcHash, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyFile 读取补丁与目标文件，成功后经临时文件 rename 原子覆盖目标。
func ApplyFile(targetPath, patchPath string) error {
	wire, err := os.ReadFile(patchPath)
	if err != nil {
		return err
	}
	target, err := os.ReadFile(targetPath)
	if err != nil {
		return err
	}
	out, err := Apply(target, wire)
	if err != nil {
		return err
	}
	tmp := targetPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, targetPath)
}

func appendU32(b []byte, v uint32) []byte {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	return append(b, t[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], v)
	return append(b, t[:]...)
}
