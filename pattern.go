package ontology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// compiledPattern 是类别约束序列编译后的匹配自动机。
//
// 约束序列形如 e0 e1 ... e(n-1)，仅 e0 与 e(n-1) 允许带「重复零次或多次」
// 标记。匹配语义等价于正则 e0[*] e1 ... e(n-1)[*] 的全串匹配。由于只有两端
// 允许重复，自动机是一条两端可能带自环与 epsilon 边的链；对其做子集构造后，
// 状态是位置集合（已取 epsilon 闭包），不同状态的数量极少。
type compiledPattern struct {
	elems []PatternElem
}

// compilePattern 校验并编译约束序列。参数非法（空序列、重复标记出现在
// 中间位置、引用未知类别）时返回包装了 ErrInvalidParams 的错误。
func compilePattern(elems []PatternElem, rank map[Category]int) (*compiledPattern, error) {
	if len(elems) == 0 {
		return nil, fmt.Errorf("%w: constraint sequence is empty", ErrInvalidParams)
	}
	for i, e := range elems {
		if e.Star && i != 0 && i != len(elems)-1 {
			return nil, fmt.Errorf("%w: repeat marker at middle position %d", ErrInvalidParams, i)
		}
		if !e.Any {
			if _, ok := rank[e.Category]; !ok {
				return nil, fmt.Errorf("%w: unknown category %q in constraint", ErrInvalidParams, e.Category)
			}
		}
	}
	return &compiledPattern{elems: elems}, nil
}

// closure 计算位置集合的 epsilon 闭包：带重复标记的位置 i 可以跳过到 i+1。
// 输入必须升序且无重复，输出保持同样性质。
func (p *compiledPattern) closure(set []int) []int {
	out := append([]int(nil), set...)
	for {
		last := out[len(out)-1]
		if last < len(p.elems) && p.elems[last].Star {
			out = append(out, last+1)
			continue
		}
		return out
	}
}

// start 返回初始状态（位置 0 的 epsilon 闭包）。
func (p *compiledPattern) start() []int {
	return p.closure([]int{0})
}

// step 返回在状态 set 上消费类别 c 后的后继状态；无法转移时返回 nil。
func (p *compiledPattern) step(set []int, c Category) []int {
	n := len(p.elems)
	var next []int
	for _, i := range set {
		if i >= n {
			continue
		}
		e := p.elems[i]
		if !e.Any && e.Category != c {
			continue
		}
		if e.Star {
			next = append(next, i) // 重复：停留在当前位置
		}
		next = append(next, i+1) // 前进到下一位置
	}
	if len(next) == 0 {
		return nil
	}
	sort.Ints(next)
	dedup := next[:1]
	for _, v := range next[1:] {
		if v != dedup[len(dedup)-1] {
			dedup = append(dedup, v)
		}
	}
	return p.closure(dedup)
}

// accepting 报告状态 set 是否为接受状态（已完整匹配约束序列）。
func (p *compiledPattern) accepting(set []int) bool {
	return len(set) > 0 && set[len(set)-1] == len(p.elems)
}

// stateKey 返回状态集合的可比较编码，用于去重与支配剪枝。
func stateKey(set []int) string {
	var b strings.Builder
	for i, v := range set {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(v))
	}
	return b.String()
}
