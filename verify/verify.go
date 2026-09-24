package verify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/chunk"
	"ontology/diff"
)

var (
	// ErrHeaderTruncated 头部不完整。
	ErrHeaderTruncated = errors.New("verify: header truncated")
	// ErrInstrTruncated 指令段不完整。
	ErrInstrTruncated = errors.New("verify: instruction section truncated")
	// ErrDataTruncated 数据段不完整。
	ErrDataTruncated = errors.New("verify: data section truncated")
	// ErrCRCMismatch CRC 不匹配。
	ErrCRCMismatch = errors.New("verify: crc mismatch")
	// ErrBlockOutOfRange 复用块号越界。
	ErrBlockOutOfRange = errors.New("verify: block index out of range")
	// ErrResultMismatch 重建结果与源整体强校验和不符。
	ErrResultMismatch = errors.New("verify: rebuilt result hash mismatch")
	// ErrFormat 其他结构类格式错误。
	ErrFormat = errors.New("verify: malformed patch")
)

const (
	magic   = "ONTP"
	version = 1
	hdrLen  = 52
)

// Encode 将逻辑补丁编码为带 CRC 的二进制补丁。
func Encode(p *diff.Patch, targetLen int) []byte {
	var inst bytes.Buffer
	var data bytes.Buffer
	for _, in := range p.Instrs {
		switch in.Op {
		case diff.OpRef:
			inst.WriteByte(1)
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(in.Block))
			inst.Write(b[:])
		case diff.OpLit:
			inst.WriteByte(2)
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(len(in.Data)))
			inst.Write(b[:])
			data.Write(in.Data)
		}
	}
	total := hdrLen + inst.Len() + data.Len() + 4
	buf := make([]byte, total)
	copy(buf[0:4], magic)
	buf[4] = version
	binary.BigEndian.PutUint32(buf[5:], uint32(p.BlockSize))
	binary.BigEndian.PutUint64(buf[9:], uint64(p.SourceLen))
	binary.BigEndian.PutUint64(buf[17:], uint64(targetLen))
	binary.BigEndian.PutUint32(buf[25:], uint32(inst.Len()))
	binary.BigEndian.PutUint32(buf[29:], uint32(data.Len()))
	copy(buf[33:49], p.SourceHash)
	copy(buf[hdrLen:], inst.Bytes())
	copy(buf[hdrLen+inst.Len():], data.Bytes())
	sum := crc32.ChecksumIEEE(buf[:total-4])
	binary.BigEndian.PutUint32(buf[total-4:], sum)
	return buf
}

// Decode 解析补丁；截断点按四类错误返回（errors.Is 可判定）。
// 返回的 patch 仅当 error == nil 时有效。
func Decode(buf []byte) (*diff.Patch, error) {
	n := len(buf)
	if n < hdrLen {
		return nil, ErrHeaderTruncated
	}
	if string(buf[0:4]) != magic || buf[4] != version {
		return nil, ErrFormat
	}
	instLen := int(binary.BigEndian.Uint32(buf[25:]))
	dataLen := int(binary.BigEndian.Uint32(buf[29:]))
	bodyEnd := hdrLen + instLen + dataLen
	if n < hdrLen+instLen {
		return nil, ErrInstrTruncated
	}
	if n < bodyEnd {
		return nil, ErrDataTruncated
	}
	if n < bodyEnd+4 {
		return nil, ErrCRCMismatch
	}
	want := binary.BigEndian.Uint32(buf[bodyEnd : bodyEnd+4])
	if crc32.ChecksumIEEE(buf[:bodyEnd]) != want {
		return nil, ErrCRCMismatch
	}

	p := &diff.Patch{
		BlockSize:  int(binary.BigEndian.Uint32(buf[5:])),
		SourceLen:  int(binary.BigEndian.Uint64(buf[9:])),
		TargetLen:  int(binary.BigEndian.Uint64(buf[17:])),
		SourceHash: bytes.Clone(buf[33:49]),
	}
	if p.BlockSize <= 0 || instLen%5 != 0 {
		return nil, ErrFormat
	}
	dataSec := buf[hdrLen+instLen : bodyEnd]
	doff := 0
	for off := hdrLen; off < hdrLen+instLen; off += 5 {
		tag := buf[off]
		v := int(binary.BigEndian.Uint32(buf[off+1 : off+5]))
		switch tag {
		case 1:
			p.Instrs = append(p.Instrs, diff.Instr{Op: diff.OpRef, Block: v})
		case 2:
			if doff+v > len(dataSec) {
				return nil, ErrFormat
			}
			p.Instrs = append(p.Instrs, diff.Instr{Op: diff.OpLit, Data: bytes.Clone(dataSec[doff : doff+v])})
			doff += v
		default:
			return nil, ErrFormat
		}
	}
	if doff != dataLen {
		return nil, ErrFormat
	}
	return p, nil
}

// Check 校验补丁可安全应用：块号范围与重建结果整体强校验和。
// numBlocks 为目标端当前块数，rebuilt 为按补丁重建出的完整数据。
func Check(p *diff.Patch, numBlocks int, rebuilt []byte) error {
	for _, in := range p.Instrs {
		if in.Op == diff.OpRef && (in.Block < 0 || in.Block >= numBlocks) {
			return ErrBlockOutOfRange
		}
	}
	if len(rebuilt) != p.SourceLen {
		return ErrResultMismatch
	}
	hash := chunk.Strong(rebuilt)
	if !bytes.Equal(hash, p.SourceHash) {
		return ErrResultMismatch
	}
	return nil
}
