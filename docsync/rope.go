package docsync

import (
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// maxLeafCodeunits 限制单个叶块的 UTF-16 码元数，保证落入叶块后的
// rune 扫描与块内二分成本有界。替换路径上的大块会被重切。
const maxLeafCodeunits = 256

// touches 是全局结构访问计数。每次下钻访问一个节点计一次，
// 测试用它证明小编辑不会逐行/逐诊断访问编辑点之前的数据。
var touches atomic.Uint64

func resetTouches() uint64 {
	old := touches.Load()
	touches.Store(0)
	return old
}

func touch(n *ropeNode) {
	if n != nil {
		touches.Add(1)
	}
}

var rngState atomic.Uint64

func nextPriority() uint64 {
	x := rngState.Add(0x9E3779B97F4A7C15)
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	return x * 0x2545F4914F6CDD1D
}

// ropeNode 是持久化（COW）文本 treap。叶节点持字符串，内部节点仅做聚合。
type ropeNode struct {
	text        string
	left, right *ropeNode
	priority    uint64
	cu          int   // 子树 UTF-16 码元总数
	nl          int   // 子树内 '\n' 总数
	leafCU      []int // 仅叶：rune 边界累计码元，长度=runecount+1
	leafNLAfter []int // 仅叶：每个 '\n' 之后的码元偏移
}

func (n *ropeNode) isLeaf() bool { return n.left == nil && n.right == nil }

func newLeaf(s string) *ropeNode {
	n := &ropeNode{text: s, priority: nextPriority()}
	n.recomputeLeaf()
	return n
}

func (n *ropeNode) recomputeLeaf() {
	cu := 0
	n.leafCU = append(n.leafCU[:0], 0)
	n.leafNLAfter = n.leafNLAfter[:0]
	for i := 0; i < len(n.text); {
		r, size := utf8.DecodeRuneInString(n.text[i:])
		if r == '\n' {
			n.leafNLAfter = append(n.leafNLAfter, cu+1)
		}
		cu += runeLen16(r)
		n.leafCU = append(n.leafCU, cu)
		i += size
	}
	n.cu = cu
	n.nl = len(n.leafNLAfter)
}

func cuOf(n *ropeNode) int {
	if n == nil {
		return 0
	}
	return n.cu
}

func nlOf(n *ropeNode) int {
	if n == nil {
		return 0
	}
	return n.nl
}

func pullRope(n *ropeNode) *ropeNode {
	if n.isLeaf() {
		n.recomputeLeaf()
		return n
	}
	n.cu = cuOf(n.left) + cuOf(n.right)
	n.nl = nlOf(n.left) + nlOf(n.right)
	return n
}

// mergeRope 合并两棵 treap（a 的内容全部在 b 之前）。
func mergeRope(a, b *ropeNode) *ropeNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	touch(a)
	touch(b)
	if a.priority > b.priority {
		if a.isLeaf() {
			return pullRope(&ropeNode{priority: a.priority, left: newLeaf(a.text), right: b})
		}
		clone := *a
		clone.right = mergeRope(a.right, b)
		return pullRope(&clone)
	}
	if b.isLeaf() {
		return pullRope(&ropeNode{priority: b.priority, left: a, right: newLeaf(b.text)})
	}
	clone := *b
	clone.left = mergeRope(a, b.left)
	return pullRope(&clone)
}

// splitRope 按 UTF-16 码元偏移 k 分裂。k 必须在 rune 边界。
func splitRope(n *ropeNode, k int) (*ropeNode, *ropeNode) {
	if n == nil {
		return nil, nil
	}
	touch(n)
	if n.isLeaf() {
		bo := byteOffsetAtCU(n.text, k)
		var a, b *ropeNode
		if k > 0 {
			a = newLeaf(n.text[:bo])
		}
		if k < n.cu {
			b = newLeaf(n.text[bo:])
		}
		return a, b
	}
	lc := cuOf(n.left)
	if k < lc {
		a, b := splitRope(n.left, k)
		clone := *n
		clone.left = b
		return a, pullRope(&clone)
	}
	if k > lc {
		a, b := splitRope(n.right, k-lc)
		clone := *n
		clone.right = a
		return pullRope(&clone), b
	}
	// k == lc：切点恰在左右子树边界，整体无需克隆（持久化共享）。
	return n.left, attachLeft(n, nil)
}

// attachLeft 返回把 n 的左孩子替换为 l 的新内部节点（右孩子与文本共享）。
func attachLeft(n, l *ropeNode) *ropeNode {
	clone := *n
	clone.left = l
	return pullRope(&clone)
}

func byteOffsetAtCU(s string, cu int) int {
	bo, c := 0, 0
	for c < cu && len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		c += runeLen16(r)
		bo += size
		s = s[size:]
	}
	return bo
}

// buildLeaves 把任意长度文本切成有界叶块并合并成 treap。
func buildLeaves(s string) *ropeNode {
	if s == "" {
		return nil
	}
	var root *ropeNode
	start := 0
	cu := 0
	flush := func(end int) {
		root = mergeRope(root, newLeaf(s[start:end]))
		start = end
		cu = 0
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		w := runeLen16(r)
		if cu > 0 && cu+w > maxLeafCodeunits {
			flush(i)
		}
		cu += w
		i += size
	}
	flush(len(s))
	return root
}

// replaceRope 把 [s,e) 替换为 text，返回新根。
func replaceRope(root *ropeNode, s, e int, text string) *ropeNode {
	a, rest := splitRope(root, s)
	_, c := splitRope(rest, e-s)
	return mergeRope(mergeRope(a, buildLeaves(text)), c)
}

func ropeString(n *ropeNode, sb *strings.Builder) {
	if n == nil {
		return
	}
	touch(n)
	if n.isLeaf() {
		sb.WriteString(n.text)
		return
	}
	ropeString(n.left, sb)
	ropeString(n.right, sb)
}

// nthNewlineOffset 返回第 idx 个 '\n'（0 起）之后的码元偏移。O(log n)。
func nthNewlineOffset(n *ropeNode, idx int) (offset int, ok bool) {
	base := 0
	for n != nil {
		touch(n)
		if n.isLeaf() {
			if idx >= n.nl {
				return 0, false
			}
			return base + n.leafNLAfter[idx], true
		}
		lnl := nlOf(n.left)
		if idx < lnl {
			n = n.left
			continue
		}
		base += cuOf(n.left)
		idx -= lnl
		n = n.right
	}
	return 0, false
}

// lineBounds 返回第 line 行（不含 '\n'）的 [start,end) 码元偏移。
func lineBounds(root *ropeNode, totalCU, line int) (start, end int, err error) {
	if line < 0 {
		return 0, 0, ErrOutOfBounds
	}
	if line == 0 {
		start = 0
	} else {
		off, ok := nthNewlineOffset(root, line-1)
		if !ok {
			return 0, 0, ErrOutOfBounds
		}
		start = off
	}
	if off, ok := nthNewlineOffset(root, line); ok {
		end = off - 1 // 换行之后的偏移减一即 '\n' 之前（行尾，不含换行符）
	} else {
		end = totalCU
	}
	return start, end, nil
}

// sliceCU 取出 [from,to) 码元片段（用于取列附近字节做代理对检测）。
func sliceCU(root *ropeNode, from, to int) string {
	var sb strings.Builder
	var walk func(n *ropeNode, lo, hi, base int)
	walk = func(n *ropeNode, lo, hi, base int) {
		if n == nil || lo >= hi {
			return
		}
		touch(n)
		if n.isLeaf() {
			relLo := lo - base
			relHi := hi - base
			if relLo < 0 {
				relLo = 0
			}
			if relHi > n.cu {
				relHi = n.cu
			}
			if relLo < relHi {
				loBo := byteOffsetAtCU(n.text, relLo)
				hiBo := byteOffsetAtCU(n.text, relHi)
				sb.WriteString(n.text[loBo:hiBo])
			}
			return
		}
		lc := cuOf(n.left)
		if lo < base+lc {
			walk(n.left, lo, min(hi, base+lc), base)
		}
		if hi > base+lc {
			walk(n.right, max(lo, base+lc), hi, base+lc)
		}
	}
	walk(root, from, to, 0)
	return sb.String()
}

// positionToOffset 校验位置并返回文档级 UTF-16 码元偏移。
func positionToOffset(root *ropeNode, totalCU int, p Position) (int, error) {
	start, end, err := lineBounds(root, totalCU, p.Line)
	if err != nil {
		return 0, err
	}
	lineLen := end - start
	col := p.Character
	if col < 0 || col > lineLen {
		return 0, ErrOutOfBounds
	}
	if col < lineLen {
		// 抽取整行（成本随行长度而非文档总行数），按 rune 边界判定列
		// 是否落在增补字符的代理对中间。
		line := sliceCU(root, start, end)
		cu := 0
		for _, r := range line {
			w := runeLen16(r)
			if w == 2 && col == cu+1 {
				return 0, ErrInsideSurrogatePair
			}
			cu += w
		}
	}
	return start + col, nil
}

func isUTF8LeadByte(b byte) bool { return b < 0x80 || b >= 0xC0 }

// offsetToPosition 把文档级 rune 边界码元偏移换算为（行，列）。
func offsetToPosition(root *ropeNode, totalCU, off int) Position {
	line := countNewlinesBefore(root, off)
	start, _, _ := lineBounds(root, totalCU, line)
	return Position{Line: line, Character: off - start}
}

// countNewlinesBefore 统计偏移 off 之前的 '\n' 数。
func countNewlinesBefore(n *ropeNode, off int) int {
	base, count := 0, 0
	for n != nil {
		touch(n)
		if n.isLeaf() {
			rel := off - base
			for _, p := range n.leafNLAfter {
				// leafNLAfter 存“换行之后”的偏移；off 之前的换行当且仅当
				// 换行之后位置 <= off（换行符本身在 off 之前或恰为 off 前一码元）。
				if p <= rel {
					count++
				} else {
					break
				}
			}
			return count
		}
		lc := cuOf(n.left)
		if off-base <= lc {
			n = n.left
			continue
		}
		count += nlOf(n.left)
		base += lc
		n = n.right
	}
	return count
}

// descendToLeaf 沿树下降，返回 off 所在叶块及块内码元偏移。
func descendToLeaf(n *ropeNode, off int) (*ropeNode, int) {
	base := 0
	for !n.isLeaf() {
		touch(n)
		lc := cuOf(n.left)
		if off-base < lc {
			n = n.left
			continue
		}
		base += lc
		n = n.right
	}
	touch(n)
	return n, off - base
}
