// Package diff 让源端按目标端签名匹配，产出最小化的补丁指令序列。
package diff

import (
	"crypto/sha256"

	"ontology/chunk"
	"ontology/sig"
)

// Instr 是一条补丁指令：复用目标端第 Index 块，或写入新数据 Data。
type Instr struct {
	Reuse bool
	Index int
	Data  []byte
}

// Patch 是源端针对目标端签名生成的补丁。
type Patch struct {
	BlockSize int
	SrcLen    int
	SrcHash   [32]byte // 源端整体 SHA-256，供目标端最终校验（不计入匹配统计）
	Instrs    []Instr
	WeakHits  int // 弱匹配次数（每次弱命中恰好触发一次强校验）
}

// NewBytes 返回补丁中新数据的总字节数。
func (p Patch) NewBytes() (n int) {
	for _, in := range p.Instrs {
		if !in.Reuse {
			n += len(in.Data)
		}
	}
	return n
}

// ReuseCount 返回复用块指令的条数。
func (p Patch) ReuseCount() (n int) {
	for _, in := range p.Instrs {
		if in.Reuse {
			n++
		}
	}
	return n
}

// Diff 用目标端签名 sg 扫描源数据 src，产出补丁。
// 弱校验和滚动筛选候选块，强校验和确认后才发复用指令；
// 未匹配的字节累积为 Data 指令。结果只依赖输入，是确定的。
func Diff(src []byte, sg sig.Signature) Patch {
	p := Patch{BlockSize: sg.BlockSize, SrcLen: len(src)}
	p.SrcHash = sha256.Sum256(src) // 完整性哈希，不属于匹配流程，不计数
	cand := make(map[uint32][]int, len(sg.Blocks))
	for i, b := range sg.Blocks {
		cand[b.Weak] = append(cand[b.Weak], i)
	}
	bs := sg.BlockSize
	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			d := make([]byte, len(lit))
			copy(d, lit)
			p.Instrs = append(p.Instrs, Instr{Data: d})
			lit = lit[:0]
		}
	}
	emit := func(j int) {
		flush()
		p.Instrs = append(p.Instrs, Instr{Reuse: true, Index: j})
	}
	n := len(src)
	var w uint32
	fresh := false
	for i := 0; i < n; {
		if rem := n - i; rem < bs { // 尾部不足一块：只对齐匹配不满的末块
			if last := len(sg.Blocks) - 1; last >= 0 && sg.BlockLen(last) == rem {
				if tail := src[i:]; chunk.WeakSum(tail) == sg.Blocks[last].Weak {
					p.WeakHits++
					if chunk.StrongSum(tail) == sg.Blocks[last].Strong {
						emit(last)
						break
					}
				}
			}
			lit = append(lit, src[i:]...)
			break
		}
		if !fresh {
			w = chunk.WeakSum(src[i : i+bs])
			fresh = true
		}
		matched := false
		for _, j := range cand[w] {
			if sg.BlockLen(j) != bs {
				continue // 不满的末块不参与全块匹配
			}
			p.WeakHits++
			if chunk.StrongSum(src[i:i+bs]) == sg.Blocks[j].Strong {
				emit(j)
				i += bs
				fresh = false
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		lit = append(lit, src[i])
		if i+bs < n { // 下一偏移仍有完整窗口才滚动
			w = chunk.Roll(w, src[i], src[i+bs])
		}
		i++
	}
	flush()
	return p
}
