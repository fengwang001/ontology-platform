// Package verify 定义补丁指令、编解码、CRC 与截断/损坏分类。
package verify

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

var pmagic = [4]byte{'O', 'P', 'A', 'T'}

// 可判定错误：四类截断及块大小、越界、结果不符、签名错误。
var (
	ErrBadBlockSize    = errors.New("bad block size")
	ErrHeaderTruncated = errors.New("patch header truncated")
	ErrInstrTruncated  = errors.New("patch instruction truncated")
	ErrDataTruncated   = errors.New("patch data segment truncated")
	ErrCRC             = errors.New("patch crc mismatch")
	ErrBlockIndex      = errors.New("block index out of range")
	ErrResultMismatch  = errors.New("result does not match source")
	ErrSignature       = errors.New("malformed signature")
)

// Op 标识补丁指令种类。
type Op uint8

const (
	OpCopy Op = 0 // 复用目标块
	OpLit  Op = 1 // 新数据
)

// Instr 是一条补丁指令。
type Instr struct {
	Op   Op
	Idx  uint32 // OpCopy 使用
	Data []byte // OpLit 使用
}

// Patch 是一份完整补丁的内存表示。
type Patch struct {
	BlockSize uint32
	SrcLen    uint64
	SrcHash   [32]byte
	Instrs    []Instr
}

// headerLen = 4 magic + 4 blockSize + 8 srcLen + 32 srcHash.
const headerLen = 48

// Encode 将补丁序列化为二进制并追加整段 CRC32(IEEE)。
func (p Patch) Encode() []byte {
	buf := make([]byte, 0, headerLen+8+32*len(p.Instrs))
	buf = append(buf, pmagic[:]...)
	var b8 [8]byte
	binary.LittleEndian.PutUint32(b8[:4], p.BlockSize)
	buf = append(buf, b8[:4]...)
	binary.LittleEndian.PutUint64(b8[:], p.SrcLen)
	buf = append(buf, b8[:]...)
	buf = append(buf, p.SrcHash[:]...)
	binary.LittleEndian.PutUint32(b8[:4], uint32(len(p.Instrs)))
	buf = append(buf, b8[:4]...)
	for _, in := range p.Instrs {
		buf = append(buf, byte(in.Op))
		switch in.Op {
		case OpCopy:
			binary.LittleEndian.PutUint32(b8[:4], in.Idx)
			buf = append(buf, b8[:4]...)
		case OpLit:
			binary.LittleEndian.PutUint32(b8[:4], uint32(len(in.Data)))
			buf = append(buf, b8[:4]...)
			buf = append(buf, in.Data...)
		}
	}
	sum := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(b8[:4], sum)
	return append(buf, b8[:4]...)
}

// Classify 解析补丁并把每个截断/损坏点映射为四类可判定错误之一。
// 严格分类：头部不完整 → 指令不完整 → 数据段不完整 → CRC 不匹配。
func Classify(raw []byte) (Patch, error) {
	if len(raw) < headerLen {
		return Patch{}, ErrHeaderTruncated
	}
	if string(raw[:4]) != string(pmagic[:]) {
		return Patch{}, ErrSignature
	}
	p := Patch{
		BlockSize: binary.LittleEndian.Uint32(raw[4:8]),
		SrcLen:    binary.LittleEndian.Uint64(raw[8:16]),
	}
	copy(p.SrcHash[:], raw[16:48])
	if p.BlockSize == 0 {
		return Patch{}, ErrBadBlockSize
	}
	if len(raw) < headerLen+4 {
		return Patch{}, ErrInstrTruncated
	}
	count := binary.LittleEndian.Uint32(raw[headerLen : headerLen+4])
	pos := headerLen + 4
	for i := uint32(0); i < count; i++ {
		if pos >= len(raw) {
			return Patch{}, ErrInstrTruncated
		}
		switch Op(raw[pos]) {
		case OpCopy:
			if pos+5 > len(raw) {
				return Patch{}, ErrInstrTruncated
			}
			p.Instrs = append(p.Instrs, Instr{Op: OpCopy, Idx: binary.LittleEndian.Uint32(raw[pos+1:])})
			pos += 5
		case OpLit:
			if pos+5 > len(raw) {
				return Patch{}, ErrInstrTruncated
			}
			n := int(binary.LittleEndian.Uint32(raw[pos+1:]))
			if pos+5+n > len(raw) {
				return Patch{}, ErrInstrTruncated
			}
			p.Instrs = append(p.Instrs, Instr{Op: OpLit, Data: append([]byte{}, raw[pos+5:pos+5+n]...)})
			pos += 5 + n
		default:
			return Patch{}, ErrSignature
		}
	}
	if pos+4 != len(raw) {
		return Patch{}, ErrDataTruncated
	}
	want := binary.LittleEndian.Uint32(raw[pos:])
	if crc32.ChecksumIEEE(raw[:pos]) != want {
		return Patch{}, ErrCRC
	}
	return p, nil
}
