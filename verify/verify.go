// Package verify 负责补丁的完整性校验、截断分类与损坏检测。
package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/diff"
)

// 可判定的错误类别，均可用 errors.Is 区分。
var (
	ErrHeaderTruncated = errors.New("verify: patch header truncated")
	ErrOpTruncated     = errors.New("verify: patch op truncated")
	ErrDataTruncated   = errors.New("verify: patch data segment truncated")
	ErrCRCMismatch     = errors.New("verify: patch crc mismatch")
	ErrBlockRange      = errors.New("verify: reuse block index out of range")
	ErrStateMismatch   = errors.New("verify: result does not match source checksum")
)

// Parse 完整解析补丁字节流并校验 CRC；任何截断都被分类为上述四类错误之一。
// 只有整份补丁结构完整且 CRC 通过时才返回指令序列。
func Parse(raw []byte) (diff.Patch, error) {
	var p diff.Patch
	if len(raw) < diff.HeaderLen {
		return p, fmt.Errorf("%w: %d < %d", ErrHeaderTruncated, len(raw), diff.HeaderLen)
	}
	if !bytes.Equal(raw[:4], diff.Magic) {
		return p, fmt.Errorf("%w: bad magic", ErrCRCMismatch)
	}
	p.BlockSize = int(binary.BigEndian.Uint32(raw[4:8]))
	p.SrcLen = int(binary.BigEndian.Uint64(raw[8:16]))
	copy(p.SrcStrong[:], raw[16:48])
	opCount := int(binary.BigEndian.Uint32(raw[48:52]))
	rest := raw[diff.HeaderLen:]
	for k := 0; k < opCount; k++ {
		if len(rest) < 1 {
			return p, fmt.Errorf("%w: op %d tag", ErrOpTruncated, k)
		}
		tag := rest[0]
		rest = rest[1:]
		switch tag {
		case 0:
			if len(rest) < 4 {
				return p, fmt.Errorf("%w: op %d index", ErrOpTruncated, k)
			}
			p.Ops = append(p.Ops, diff.Op{Reuse: true, Index: int(binary.BigEndian.Uint32(rest[:4]))})
			rest = rest[4:]
		case 1:
			if len(rest) < 4 {
				return p, fmt.Errorf("%w: op %d length", ErrOpTruncated, k)
			}
			n := int(binary.BigEndian.Uint32(rest[:4]))
			rest = rest[4:]
			if len(rest) < n {
				return p, fmt.Errorf("%w: op %d payload %d < %d", ErrDataTruncated, k, len(rest), n)
			}
			p.Ops = append(p.Ops, diff.Op{Data: rest[:n]})
			rest = rest[n:]
		default:
			return p, fmt.Errorf("%w: unknown op tag %d", ErrCRCMismatch, tag)
		}
	}
	if len(rest) != 4 {
		return p, fmt.Errorf("%w: crc trailer %d bytes", ErrCRCMismatch, len(rest))
	}
	want := binary.BigEndian.Uint32(rest)
	if got := crc32.ChecksumIEEE(raw[:len(raw)-4]); got != want {
		return p, fmt.Errorf("%w: %08x != %08x", ErrCRCMismatch, got, want)
	}
	return p, nil
}

// ValidateOps 校验所有复用指令的块号都在目标端块数范围内。
func ValidateOps(p diff.Patch, targetBlocks int) error {
	for k, op := range p.Ops {
		if op.Reuse && (op.Index < 0 || op.Index >= targetBlocks) {
			return fmt.Errorf("%w: op %d index %d, blocks %d", ErrBlockRange, k, op.Index, targetBlocks)
		}
	}
	return nil
}

// CheckResult 校验应用结果的长度与整体强校验和是否等于源端。
func CheckResult(p diff.Patch, result []byte) error {
	if len(result) != p.SrcLen {
		return fmt.Errorf("%w: length %d != %d", ErrStateMismatch, len(result), p.SrcLen)
	}
	if sha256.Sum256(result) != p.SrcStrong {
		return fmt.Errorf("%w: strong checksum differs", ErrStateMismatch)
	}
	return nil
}
