package diff

import (
	"bytes"

	"ontology/chunk"
	"ontology/sig"
)

// Op 标识指令类型。
type Op uint8

const (
	// OpRef 引用目标端已有块。
	OpRef Op = 1
	// OpLit 内联字面数据。
	OpLit Op = 2
)

// Instr 是一条补丁指令。
type Instr struct {
	Op    Op
	Block int    // OpRef 使用
	Data  []byte // OpLit 使用
}

// Patch 是源端产出的逻辑补丁（编码由 verify 负责）。
type Patch struct {
	BlockSize  int
	SourceLen  int
	TargetLen  int
	SourceHash []byte
	Instrs     []Instr
}

// Stats 记录匹配过程的代价指标。
type Stats struct {
	WeakOps     int
	WeakHits    int
	StrongCount int
}

// Build 基于目标签名对源数据做滚动差分。
func Build(source []byte, s *sig.Signature) (*Patch, Stats) {
	bs := s.BlockSize
	r, _ := chunk.NewRoller(source, bs)
	var instrs []Instr
	var lit []byte
	hits := 0

	flushLit := func() {
		if len(lit) > 0 {
			instrs = append(instrs, Instr{Op: OpLit, Data: lit})
			lit = nil
		}
	}

	// i 为当前窗口起始偏移；窗口完整区间为 [0, n-bs]。
	i := 0
	n := len(source)
	for i+bs <= n {
		matched := -1
		if ids := s.Lookup(r.Weak()); len(ids) > 0 {
			hits++
			strong := r.ConfirmStrong()
			for _, id := range ids {
				if bytes.Equal(s.Entries[id].Strong, strong) {
					matched = id
					break
				}
			}
		}
		if matched >= 0 {
			flushLit()
			instrs = append(instrs, Instr{Op: OpRef, Block: matched})
			// 跳过整块：r 连续推进 bs 次（受边界限制）。
			for j := 0; j < bs && r.Valid(); j++ {
				r.Advance()
			}
			i += bs
			continue
		}
		lit = append(lit, source[i])
		i++
		r.Advance()
	}
	lit = append(lit, source[i:]...)
	flushLit()

	p := &Patch{
		BlockSize:  bs,
		SourceLen:  len(source),
		SourceHash: chunk.Strong(source),
		Instrs:     instrs,
	}
	return p, Stats{WeakOps: r.WeakOps(), WeakHits: hits, StrongCount: r.StrongCount()}
}

// SetTargetLen 记录目标长度用于补丁头。
func (p *Patch) SetTargetLen(n int) { p.TargetLen = n }

// RefCount 返回复用块指令条数。
func (p *Patch) RefCount() int {
	n := 0
	for _, in := range p.Instrs {
		if in.Op == OpRef {
			n++
		}
	}
	return n
}

// LiteralBytes 返回字面数据总字节数。
func (p *Patch) LiteralBytes() int {
	n := 0
	for _, in := range p.Instrs {
		if in.Op == OpLit {
			n += len(in.Data)
		}
	}
	return n
}
