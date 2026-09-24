// Package diff 在源端按目标端签名滚动匹配，产出最小补丁指令序列
// （复用目标端块 / 新数据），并统计弱命中与强校验次数。
package diff

import (
	"encoding/binary"

	"ontology/chunk"
	"ontology/sig"
	"ontology/verify"
)

// InstrKind 区分补丁指令类型。
type InstrKind byte

const (
	// Copy 复用目标端的第 Index 块。
	Copy InstrKind = 0
	// Literal 携带新数据 Data。
	Literal InstrKind = 1
)

// Instr 是一条补丁指令。
type Instr struct {
	Kind  InstrKind
	Index uint32
	Data  []byte
}

// Stats 记录一次 Diff 的匹配统计。
type Stats struct {
	WeakMatches    int // 弱校验和命中（含碰撞）次数
	StrongComputed int // 强校验和计算次数，只在弱命中时发生
}

// Patch 是源端针对目标端签名产出的补丁。
type Patch struct {
	BlockSize int
	SrcLen    int
	SrcStrong chunk.Strong
	Instrs    []Instr
	Stats     Stats
}

// NewDataBytes 返回补丁中新数据的总字节数。
func (p *Patch) NewDataBytes() int {
	n := 0
	for _, in := range p.Instrs {
		if in.Kind == Literal {
			n += len(in.Data)
		}
	}
	return n
}

// ReusedBlocks 返回补丁中复用目标端块的条数。
func (p *Patch) ReusedBlocks() int {
	n := 0
	for _, in := range p.Instrs {
		if in.Kind == Copy {
			n++
		}
	}
	return n
}

// Diff 用目标端签名 s 扫描源数据 src，产出补丁。匹配是确定性的：
// 只按偏移顺序扫描、按切片顺序查表，不依赖 map 迭代顺序。
func Diff(src []byte, s *sig.Signature) *Patch {
	p := &Patch{
		BlockSize: s.BlockSize,
		SrcLen:    len(src),
		SrcStrong: chunk.SumStrong(src),
	}
	byWeak := make(map[uint32][]int, len(s.Blocks))
	for i, b := range s.Blocks {
		byWeak[b.Weak] = append(byWeak[b.Weak], i)
	}
	n := s.BlockSize
	literalStart := 0
	flush := func(end int) {
		if end > literalStart {
			p.Instrs = append(p.Instrs, Instr{Kind: Literal, Data: src[literalStart:end]})
		}
	}
	off := 0
	if len(src) >= n {
		w := chunk.SumWeak(src[:n])
		for off+n <= len(src) {
			if idxs, ok := byWeak[w.Value()]; ok {
				p.Stats.WeakMatches++
				strong := chunk.SumStrong(src[off : off+n])
				p.Stats.StrongComputed++
				matched := -1
				for _, idx := range idxs {
					if s.Blocks[idx].Strong == strong {
						matched = idx
						break
					}
				}
				if matched >= 0 {
					flush(off)
					p.Instrs = append(p.Instrs, Instr{Kind: Copy, Index: uint32(matched)})
					off += n
					literalStart = off
					if off+n <= len(src) {
						w = chunk.SumWeak(src[off : off+n])
					}
					continue
				}
			}
			if off+n < len(src) {
				w = w.Roll(src[off], src[off+n], n)
			}
			off++
		}
	}
	// 末块可能不满：源数据尾部若与目标端末块等长且校验相同，直接复用。
	if tail := src[literalStart:]; len(tail) > 0 && len(tail) <= n && len(s.Blocks) > 0 {
		last := len(s.Blocks) - 1
		if chunk.SumWeak(tail).Value() == s.Blocks[last].Weak {
			p.Stats.WeakMatches++
			p.Stats.StrongComputed++
			if chunk.SumStrong(tail) == s.Blocks[last].Strong {
				p.Instrs = append(p.Instrs, Instr{Kind: Copy, Index: uint32(last)})
				return p
			}
		}
	}
	flush(len(src))
	return p
}

var magic = []byte("ONTP")

const (
	version   = 1
	HeaderLen = 4 + 1 + 4 + 8 + 16 + 4 + 4 // magic+ver+blockSize+srcLen+srcStrong+numInstr+instrLen
)

// Encode 把补丁编码为确定的字节流（大端），末尾附 CRC32（由 patch 包校验）。
func (p *Patch) Encode() []byte {
	out := make([]byte, 0, HeaderLen+len(p.Instrs)*5+p.NewDataBytes()+4)
	out = append(out, magic...)
	out = append(out, version)
	out = binary.BigEndian.AppendUint32(out, uint32(p.BlockSize))
	out = binary.BigEndian.AppendUint64(out, uint64(p.SrcLen))
	out = append(out, p.SrcStrong[:]...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(p.Instrs)))
	instrPos := len(out)
	out = append(out, 0, 0, 0, 0) // instrLen 占位，最后回填
	instrStart := len(out)
	for _, in := range p.Instrs {
		out = append(out, byte(in.Kind))
		if in.Kind == Copy {
			out = binary.BigEndian.AppendUint32(out, in.Index)
		} else {
			out = binary.BigEndian.AppendUint32(out, uint32(len(in.Data)))
			out = append(out, in.Data...)
		}
	}
	binary.BigEndian.PutUint32(out[instrPos:instrPos+4], uint32(len(out)-instrStart))
	return verify.Seal(out)
}
