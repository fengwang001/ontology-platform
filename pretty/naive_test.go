package pretty

import (
	"fmt"
	"strings"
)

// 本文件是用于对照测试的独立朴素模型：它直接按规格递归实现，不使用
// 被测引擎的扫描剖面、级联断开等任何优化。对每个组，它按定义计算
// "按平铺渲染并连同其后续内容一起、直到第一个处于断开模式的可断点或
// 强制换行（或文档结束）为止"的累计宽度，据此穷举平铺与断开两种
// 候选并按规则校验取舍（含强制换行的组一律断开）。输出按行构造并用
// strings.TrimRight 去行尾空格，与被测引擎的增量裁剪实现相互独立。

// naiveItem 是朴素模型工作清单条目。
type naiveItem struct {
	ind int
	m   mode
	d   *node
}

// naive 是朴素渲染器。
type naive struct {
	width int
	frags map[string]Doc
	log   *strings.Builder

	lines   []string
	cur     strings.Builder
	col     int
	ind     int
	pending bool
}

// naiveRender 用朴素模型渲染文档，返回结果与逐组判定依据日志。
func naiveRender(d Doc, width int, frags map[string]Doc, log *strings.Builder) Result {
	n := &naive{width: width, frags: frags, log: log, pending: true}
	items := []naiveItem{{ind: 0, m: modeBreak, d: asNode(d)}}
	for len(items) > 0 {
		e := items[len(items)-1]
		items = items[:len(items)-1]
		switch e.d.kind {
		case kindText:
			n.emit(e.d.text)
		case kindCond:
			if e.m == modeFlat {
				n.emit(e.d.text)
			} else {
				n.emit(e.d.alt)
			}
		case kindSpace:
			if e.m == modeFlat {
				n.emit(" ")
			} else {
				n.newline(e.ind)
			}
		case kindBlank:
			if e.m == modeBreak {
				n.newline(e.ind)
			}
		case kindHardLine:
			n.newline(e.ind)
		case kindSeq:
			for i := len(e.d.children) - 1; i >= 0; i-- {
				items = append(items, naiveItem{e.ind, e.m, e.d.children[i]})
			}
		case kindIndent:
			items = append(items, naiveItem{e.ind + e.d.n, e.m, e.d.child})
		case kindAlign:
			items = append(items, naiveItem{n.col, e.m, e.d.child})
		case kindRef:
			items = append(items, naiveItem{e.ind, e.m, asNode(n.frags[e.d.name])})
		case kindGroup:
			items = append(items, n.decide(e, items))
		}
	}
	n.lines = append(n.lines, strings.TrimRight(n.cur.String(), " "))
	text := strings.Join(n.lines, "\n")
	var over []OverlongLine
	for i, ln := range n.lines {
		if w := Width(ln); w > width {
			over = append(over, OverlongLine{Line: i + 1, Width: w})
		}
	}
	return Result{Text: text, Overlong: over}
}

// decide 为一个组做平铺/断开决策并记录判定依据。
func (n *naive) decide(e naiveItem, rest []naiveItem) naiveItem {
	if e.m == modeFlat {
		fmt.Fprintf(n.log, "  组@%d列: 外层已平铺 → 平铺\n", n.col)
		return naiveItem{e.ind, modeFlat, e.d.child}
	}
	if n.hasHardLine(e.d.child) {
		fmt.Fprintf(n.log, "  组@%d列: 含强制换行 → 断开（不做放得下的判定）\n", n.col)
		return naiveItem{e.ind, modeBreak, e.d.child}
	}
	w := n.fitsWidth(e.d.child, rest)
	if n.col+w <= n.width {
		fmt.Fprintf(n.log, "  组@%d列: 平铺累计宽=%d, %d+%d<=%d → 平铺\n", n.col, w, n.col, w, n.width)
		return naiveItem{e.ind, modeFlat, e.d.child}
	}
	fmt.Fprintf(n.log, "  组@%d列: 平铺累计宽=%d, %d+%d>%d → 断开\n", n.col, w, n.col, w, n.width)
	return naiveItem{e.ind, modeBreak, e.d.child}
}

// fitsWidth 按定义完整累计"组按平铺渲染并连同其后续内容"的宽度，
// 直到第一个处于断开模式的可断点或强制换行（或文档结束）为止。
func (n *naive) fitsWidth(child *node, rest []naiveItem) int {
	w, stop := n.scan(modeFlat, child)
	total := w
	if stop {
		return total
	}
	for i := len(rest) - 1; i >= 0; i-- {
		w, stop := n.scan(rest[i].m, rest[i].d)
		total += w
		if stop {
			return total
		}
	}
	return total
}

// scan 递归计算"从当前列扫描节点直到第一个断开的可断点或强制换行
// （或子树结束）"的累计宽度与是否中途停止。尚未轮到决策的组按平铺
// 计入宽度。
func (n *naive) scan(m mode, d *node) (int, bool) {
	switch d.kind {
	case kindText:
		return Width(d.text), false
	case kindCond:
		if m == modeFlat {
			return Width(d.text), false
		}
		return Width(d.alt), false
	case kindSpace:
		if m == modeFlat {
			return 1, false
		}
		return 0, true
	case kindBlank:
		if m == modeFlat {
			return 0, false
		}
		return 0, true
	case kindHardLine:
		return 0, true
	case kindIndent, kindAlign:
		return n.scan(m, d.child)
	case kindGroup:
		return n.scan(modeFlat, d.child)
	case kindRef:
		return n.scan(m, asNode(n.frags[d.name]))
	case kindSeq:
		total := 0
		for _, c := range d.children {
			w, stop := n.scan(m, c)
			total += w
			if stop {
				return total, true
			}
		}
		return total, false
	}
	return 0, false
}

// hasHardLine 递归检查子树（含引用展开）是否含强制换行。
func (n *naive) hasHardLine(d *node) bool {
	switch d.kind {
	case kindHardLine:
		return true
	case kindIndent, kindAlign, kindGroup:
		return n.hasHardLine(d.child)
	case kindSeq:
		for _, c := range d.children {
			if n.hasHardLine(c) {
				return true
			}
		}
	case kindRef:
		return n.hasHardLine(asNode(n.frags[d.name]))
	}
	return false
}

// emit 输出一段文本；行首先写缩进。
func (n *naive) emit(s string) {
	if s == "" {
		return
	}
	if n.pending {
		n.cur.WriteString(strings.Repeat(" ", n.ind))
		n.col = n.ind
		n.pending = false
	}
	n.cur.WriteString(s)
	n.col += Width(s)
}

// newline 结束当前行（去行尾空格）并以缩进 ind 开始新行。
func (n *naive) newline(ind int) {
	n.lines = append(n.lines, strings.TrimRight(n.cur.String(), " "))
	n.cur.Reset()
	n.ind = ind
	n.col = ind
	n.pending = true
}

// sprintDoc 以 S 表达式打印文档树，用于测试日志。
func sprintDoc(d Doc) string {
	var sb strings.Builder
	writeDoc(&sb, asNode(d))
	return sb.String()
}

func writeDoc(sb *strings.Builder, n *node) {
	switch n.kind {
	case kindText:
		fmt.Fprintf(sb, "%q", n.text)
	case kindSpace:
		sb.WriteString("sp")
	case kindBlank:
		sb.WriteString("bl")
	case kindHardLine:
		sb.WriteString("hl")
	case kindIndent:
		fmt.Fprintf(sb, "(ind %d ", n.n)
		writeDoc(sb, n.child)
		sb.WriteString(")")
	case kindAlign:
		sb.WriteString("(align ")
		writeDoc(sb, n.child)
		sb.WriteString(")")
	case kindGroup:
		sb.WriteString("(grp ")
		writeDoc(sb, n.child)
		sb.WriteString(")")
	case kindCond:
		fmt.Fprintf(sb, "(cond %q %q)", n.text, n.alt)
	case kindRef:
		fmt.Fprintf(sb, "(ref %s)", n.name)
	case kindSeq:
		sb.WriteString("(seq")
		for _, c := range n.children {
			sb.WriteString(" ")
			writeDoc(sb, c)
		}
		sb.WriteString(")")
	}
}
