// Package patch 在目标端解码并应用补丁。应用是原子的：先完整解码、
// 校验 CRC 与指令合法性，在新缓冲区拼装结果并比对整体强校验和，
// 全部通过才返回；任何失败都不会改动目标端数据。
package patch

import (
	"encoding/binary"
	"fmt"

	"ontology/chunk"
	"ontology/diff"
	"ontology/verify"
)

var magic = []byte("ONTP")

// Decode 把补丁字节流解析为 *diff.Patch，并按截断位置分类报错：
// 头部 / 指令 / 数据段不完整或 CRC 不匹配。
func Decode(data []byte) (*diff.Patch, error) {
	if len(data) < diff.HeaderLen {
		return nil, verify.ErrTruncatedHeader
	}
	if string(data[:4]) != string(magic) || data[4] != 1 {
		return nil, fmt.Errorf("%w: bad magic or version", verify.ErrTruncatedHeader)
	}
	p := &diff.Patch{
		BlockSize: int(binary.BigEndian.Uint32(data[5:9])),
		SrcLen:    int(binary.BigEndian.Uint64(data[9:17])),
	}
	copy(p.SrcStrong[:], data[17:33])
	numInstr := binary.BigEndian.Uint32(data[33:37])
	instrLen := binary.BigEndian.Uint32(data[37:41])
	secEnd := diff.HeaderLen + int(instrLen)
	pos := diff.HeaderLen
	avail := len(data)
	for i := uint32(0); i < numInstr; i++ {
		if pos+1 > secEnd || pos+1 > avail {
			return nil, verify.ErrTruncatedInstruction
		}
		op := diff.InstrKind(data[pos])
		pos++
		switch op {
		case diff.Copy:
			if pos+4 > secEnd || pos+4 > avail {
				return nil, verify.ErrTruncatedInstruction
			}
			idx := binary.BigEndian.Uint32(data[pos : pos+4])
			pos += 4
			p.Instrs = append(p.Instrs, diff.Instr{Kind: diff.Copy, Index: idx})
		case diff.Literal:
			if pos+4 > secEnd || pos+4 > avail {
				return nil, verify.ErrTruncatedInstruction
			}
			dl := int(binary.BigEndian.Uint32(data[pos : pos+4]))
			pos += 4
			if pos+dl > secEnd || pos+dl > avail {
				return nil, verify.ErrTruncatedData
			}
			p.Instrs = append(p.Instrs, diff.Instr{Kind: diff.Literal, Data: data[pos : pos+dl]})
			pos += dl
		default:
			return nil, fmt.Errorf("%w: unknown op %d", verify.ErrTruncatedInstruction, op)
		}
	}
	if pos != secEnd {
		return nil, fmt.Errorf("%w: instruction section length mismatch", verify.ErrTruncatedInstruction)
	}
	if len(data) != secEnd+4 {
		return nil, verify.ErrCRCMismatch
	}
	if _, err := verify.Open(data[:secEnd+4]); err != nil {
		return nil, err
	}
	return p, nil
}

// Apply 解码补丁并应用到 target，返回与源端一致的新数据。
// 任一校验失败都返回错误，target 保持逐字节不变。
func Apply(target, patchBytes []byte) ([]byte, error) {
	p, err := Decode(patchBytes)
	if err != nil {
		return nil, err
	}
	numBlocks, err := chunk.NumBlocks(len(target), p.BlockSize)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, p.SrcLen)
	for _, in := range p.Instrs {
		if in.Kind == diff.Copy {
			if in.Index >= uint32(numBlocks) {
				return nil, fmt.Errorf("%w: block %d of %d", verify.ErrBlockOutOfRange, in.Index, numBlocks)
			}
			out = append(out, chunk.Block(target, p.BlockSize, int(in.Index))...)
		} else {
			out = append(out, in.Data...)
		}
	}
	if len(out) != p.SrcLen {
		return nil, fmt.Errorf("%w: length %d != %d", verify.ErrChecksumMismatch, len(out), p.SrcLen)
	}
	if err := verify.CheckResult(out, p.SrcStrong); err != nil {
		return nil, err
	}
	return out, nil
}
