// Package patch 负责补丁文件的编解码与在目标端的原子应用。
package patch

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/chunk"
	"ontology/diff"
	"ontology/sig"
)

// 四类可判定的补丁损坏/越界错误，均可用 errors.Is 区分。
var (
	ErrTruncatedHeader = errors.New("patch: truncated header")
	ErrTruncatedInstr  = errors.New("patch: truncated instructions")
	ErrTruncatedData   = errors.New("patch: truncated data segment")
	ErrCRCMismatch     = errors.New("patch: CRC mismatch")
	ErrBlockOutOfRange = errors.New("patch: block index out of range")
)

const (
	magic     = "OSYN"
	headerLen = 4 + 4 + 8 + 32 + 4 // magic | blockSize | srcLen | srcHash | instrCount
	instrLen  = 1 + 4              // type | value
)

// Encode 把补丁序列化为：头部 | 指令区 | 数据段 | CRC32（覆盖其前全部字节）。
func Encode(p diff.Patch) []byte {
	out := make([]byte, 0, headerLen+len(p.Instrs)*instrLen+p.NewBytes()+4)
	out = append(out, magic...)
	out = binary.BigEndian.AppendUint32(out, uint32(p.BlockSize))
	out = binary.BigEndian.AppendUint64(out, uint64(p.SrcLen))
	out = append(out, p.SrcHash[:]...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(p.Instrs)))
	for _, in := range p.Instrs {
		if in.Reuse {
			out = append(out, 0)
			out = binary.BigEndian.AppendUint32(out, uint32(in.Index))
		} else {
			out = append(out, 1)
			out = binary.BigEndian.AppendUint32(out, uint32(len(in.Data)))
		}
	}
	for _, in := range p.Instrs {
		if !in.Reuse {
			out = append(out, in.Data...)
		}
	}
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// Decode 解析补丁文件。任何截断都被分类为头部/指令/数据段/CRC 之一，
// 绝不返回可部分使用的补丁。
func Decode(b []byte) (diff.Patch, error) {
	var p diff.Patch
	if len(b) < headerLen || string(b[:4]) != magic {
		return p, ErrTruncatedHeader
	}
	p.BlockSize = int(binary.BigEndian.Uint32(b[4:8]))
	p.SrcLen = int(binary.BigEndian.Uint64(b[8:16]))
	copy(p.SrcHash[:], b[16:48])
	count := int(binary.BigEndian.Uint32(b[48:52]))
	off := headerLen
	if count < 0 || count > (len(b)-off)/instrLen {
		return diff.Patch{}, ErrTruncatedInstr
	}
	p.Instrs = make([]diff.Instr, count)
	dataLen := 0
	for k := range p.Instrs {
		if off+instrLen > len(b) {
			return diff.Patch{}, ErrTruncatedInstr
		}
		typ := b[off]
		v := int(binary.BigEndian.Uint32(b[off+1 : off+5]))
		off += instrLen
		if typ == 0 {
			p.Instrs[k] = diff.Instr{Reuse: true, Index: v}
		} else {
			p.Instrs[k] = diff.Instr{Data: make([]byte, v)}
			dataLen += v
		}
	}
	if len(b)-off < dataLen {
		return diff.Patch{}, ErrTruncatedData
	}
	pos := off
	for k := range p.Instrs {
		if !p.Instrs[k].Reuse {
			copy(p.Instrs[k].Data, b[pos:pos+len(p.Instrs[k].Data)])
			pos += len(p.Instrs[k].Data)
		}
	}
	off += dataLen
	if len(b)-off < 4 || crc32.ChecksumIEEE(b[:off]) != binary.BigEndian.Uint32(b[off:off+4]) {
		return diff.Patch{}, ErrCRCMismatch
	}
	return p, nil
}

// Apply 先校验全部指令再落地：任何失败都不改动 target，结果写入新切片返回。
func Apply(target []byte, p diff.Patch) ([]byte, error) {
	if p.BlockSize <= 0 {
		return nil, sig.ErrZeroBlockSize
	}
	nb := chunk.NumBlocks(len(target), p.BlockSize)
	total := 0
	for _, in := range p.Instrs { // 第一遍：纯校验，不产生任何副作用
		if in.Reuse {
			if in.Index < 0 || in.Index >= nb {
				return nil, ErrBlockOutOfRange
			}
			total += chunk.BlockLen(len(target), p.BlockSize, in.Index)
		} else {
			total += len(in.Data)
		}
	}
	out := make([]byte, 0, total)
	for _, in := range p.Instrs { // 第二遍：构建完整结果
		if in.Reuse {
			out = append(out, chunk.Block(target, p.BlockSize, in.Index)...)
		} else {
			out = append(out, in.Data...)
		}
	}
	return out, nil
}
