// Package diff 在源端按目标端签名匹配，产出最小补丁指令（复用块 / 新数据）。
package diff

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"

	"ontology/chunk"
	"ontology/sig"
)

// Magic 是补丁文件头魔数，verify 解析时校验。
var Magic = []byte("OSP1")

// HeaderLen 是补丁头部长度：magic + bs + srcLen + srcStrong + opCount。
const HeaderLen = 4 + 4 + 8 + 32 + 4

// Op 是一条补丁指令：复用目标端第 Index 块，或写入字面量 Data。
type Op struct {
	Reuse bool
	Index int
	Data  []byte
}

// Patch 描述把目标端数据变为源端数据所需的全部信息。
type Patch struct {
	BlockSize int
	SrcLen    int
	SrcStrong [32]byte
	Ops       []Op
}

// Stats 记录 diff 过程的统计量，供复杂度与最小性断言。
type Stats struct {
	WeakHits    int // 弱校验和命中（进而计算强校验）的次数
	NewBytes    int // 补丁中的新数据字节数
	ReuseBlocks int // 复用块数
}

// Diff 用目标端签名 s 扫描源数据 src，产出补丁与统计。
func Diff(src []byte, s sig.Signature) (Patch, Stats, error) {
	var p Patch
	var st Stats
	bs := s.BlockSize
	if bs <= 0 {
		return p, st, chunk.ErrBlockSize
	}
	index := make(map[uint32][]int, len(s.Entries))
	for i, e := range s.Entries {
		index[e.Weak] = append(index[e.Weak], i)
	}
	p.BlockSize, p.SrcLen, p.SrcStrong = bs, len(src), sha256.Sum256(src)
	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			p.Ops = append(p.Ops, Op{Data: lit})
			st.NewBytes += len(lit)
			lit = nil
		}
	}
	i, have := 0, false
	var w uint32
	for i+bs <= len(src) {
		if !have {
			w = chunk.Weak(src[i : i+bs])
			have = true
		}
		matched := false
		if cands, ok := index[w]; ok {
			st.WeakHits++
			strong := chunk.Strong(src[i : i+bs])
			for _, c := range cands {
				if s.Entries[c].Strong == strong {
					flush()
					p.Ops = append(p.Ops, Op{Reuse: true, Index: c})
					st.ReuseBlocks++
					i, have, matched = i+bs, false, true
					break
				}
			}
		}
		if !matched {
			lit = append(lit, src[i])
			if i+bs < len(src) {
				w = chunk.RollSum(w, src[i], src[i+bs])
			}
			i++
		}
	}
	lit = append(lit, src[i:]...)
	flush()
	return p, st, nil
}

// Encode 把补丁序列化为：头部 + 指令序列 + CRC32 尾部。
func (p Patch) Encode() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, HeaderLen))
	buf.Write(Magic)
	binary.Write(buf, binary.BigEndian, uint32(p.BlockSize))
	binary.Write(buf, binary.BigEndian, uint64(p.SrcLen))
	buf.Write(p.SrcStrong[:])
	binary.Write(buf, binary.BigEndian, uint32(len(p.Ops)))
	for _, op := range p.Ops {
		if op.Reuse {
			buf.WriteByte(0)
			binary.Write(buf, binary.BigEndian, uint32(op.Index))
		} else {
			buf.WriteByte(1)
			binary.Write(buf, binary.BigEndian, uint32(len(op.Data)))
			buf.Write(op.Data)
		}
	}
	raw := buf.Bytes()
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(raw))
	return append(raw, crc[:]...)
}
