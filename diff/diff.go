// Package diff 让源端按目标端签名滚动匹配，产出最小补丁指令。
package diff

import (
	"ontology/chunk"
	"ontology/patch"
	"ontology/sig"
)

// Stats 记录一次 diff 的计数器，用于验证复杂度约束。
type Stats struct {
	WeakLookups  int // 滚动窗口弱查询次数
	WeakMatches  int // 弱命中（存在候选块）的偏移数
	StrongChecks int // 强校验计算次数（只在弱命中时发生）
	NewBytes     int // 补丁中新数据字节数
	ReusedBlocks int // 复用块指令数
}

// Compute 用目标端签名 sg 扫描源数据，产出补丁与统计。
// 弱校验和滚动筛选，强校验和只在弱命中时确认，结果确定性输出。
func Compute(src []byte, sg sig.Signature) (patch.Patch, Stats, error) {
	var st Stats
	p := patch.Patch{
		BlockSize: sg.BlockSize,
		SrcLen:    len(src),
		SrcHash:   chunk.Strong(src),
	}
	bs := sg.BlockSize
	if bs <= 0 {
		return p, st, chunk.ErrBlockSize
	}
	byWeak := make(map[uint32][]int, len(sg.Weak))
	for i, w := range sg.Weak {
		byWeak[w] = append(byWeak[w], i)
	}
	var pending []byte
	flush := func() {
		if len(pending) == 0 {
			return
		}
		p.Instrs = append(p.Instrs, patch.Instr{Op: patch.OpData, Data: pending})
		st.NewBytes += len(pending)
		pending = nil
	}
	i := 0
	var roll *chunk.Roller
	if len(src) >= bs {
		roll = chunk.NewRoller(src[:bs])
	}
	for i+bs <= len(src) {
		st.WeakLookups++
		if cands, ok := byWeak[roll.Sum()]; ok {
			st.WeakMatches++
			st.StrongChecks++
			h := chunk.Strong(src[i : i+bs])
			matched := false
			for _, c := range cands {
				if sg.Strong[c] == h {
					flush()
					p.Instrs = append(p.Instrs, patch.Instr{Op: patch.OpReuse, Block: c})
					st.ReusedBlocks++
					i += bs
					matched = true
					break
				}
			}
			if matched {
				if i+bs <= len(src) {
					roll = chunk.NewRoller(src[i : i+bs])
				}
				continue
			}
		}
		pending = append(pending, src[i])
		if i+bs+1 <= len(src) {
			roll.Roll(src[i], src[i+bs])
		}
		i++
	}
	pending = append(pending, src[i:]...)
	flush()
	return p, st, nil
}
