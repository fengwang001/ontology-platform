package pretty

import "bytes"

// 本文件实现迭代式渲染引擎。引擎把工作清单（显式栈）中的条目逐个
// 弹出处理：每个条目携带缩进量、模式（平铺/断开）与文档节点。组的
// 平铺/断开决策在条目被弹出时做出，放得下判定借助 analyze 通道预算
// 的扫描剖面以 O(1) 完成单个节点的考察。全程无递归，不会因深度栈溢出。

// mode 是渲染模式：modeFlat 表示处于已判定平铺的组内，modeBreak 表示
// 处于断开上下文（顶层或已断开的组的直接内容）。
type mode uint8

const (
	modeBreak mode = iota
	modeFlat
)

// entry 是工作清单条目。
type entry struct {
	ind int
	m   mode
	d   *node
}

// indentCap 是缩进量的饱和上限。缩进量超过它时渲染结果必然超过输出
// 上限，饱和不影响可观察行为。
const indentCap = MaxOutputWidth + MaxWidth + 1

// renderer 是单次渲染的状态。
type renderer struct {
	width int
	info  map[*node]nodeInfo
	frags map[string]Doc

	buf        bytes.Buffer
	col        int  // 当前列（行首待写缩进时等于缩进量）
	ind        int  // 当前行的缩进量
	lineW      int  // 当前行已写入的宽度
	lineTrail  int  // 当前行末尾连续空格的个数（行宽=空格宽=1）
	lineNo     int  // 当前行号，从 1 开始
	total      int  // 已输出内容的总宽度（换行符计 1）
	pendingInd bool // 是否处于尚未写入缩进的行首
	tooLarge   bool // 输出是否已超限

	overlong []OverlongLine

	// 级联断开优化：组因放得下判定失败而断开、且其内容（剥开
	// 缩进/对齐/引用后）直接是另一个组时，内层组在同一列面对的
	// 考察序列与外层完全相同，可直接断开而无需重新扫描。
	cascadeOK  bool
	cascadeCol int
}

// render 渲染文档树。info 为 analyze 的标注结果，frags 为会话快照。
func render(root *node, width int, info map[*node]nodeInfo, frags map[string]Doc) (Result, *Error) {
	r := &renderer{
		width:      width,
		info:       info,
		frags:      frags,
		lineNo:     1,
		pendingInd: true,
	}
	stack := []entry{{ind: 0, m: modeBreak, d: root}}
	for len(stack) > 0 && !r.tooLarge {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch e.d.kind {
		case kindText:
			r.emit(e.d.text)
		case kindCond:
			if e.m == modeFlat {
				r.emit(e.d.text)
			} else {
				r.emit(e.d.alt)
			}
		case kindSpace:
			if e.m == modeFlat {
				r.emit(" ")
			} else {
				r.newline(e.ind)
			}
		case kindBlank:
			if e.m == modeBreak {
				r.newline(e.ind)
			}
		case kindHardLine:
			r.newline(e.ind)
		case kindSeq:
			for i := len(e.d.children) - 1; i >= 0; i-- {
				stack = append(stack, entry{e.ind, e.m, e.d.children[i]})
			}
		case kindIndent:
			ind := e.ind + e.d.n
			if ind < 0 || ind > indentCap {
				ind = indentCap
			}
			stack = append(stack, entry{ind, e.m, e.d.child})
		case kindAlign:
			stack = append(stack, entry{r.col, e.m, e.d.child})
		case kindRef:
			stack = append(stack, entry{e.ind, e.m, asNode(r.frags[e.d.name])})
		case kindGroup:
			stack = r.decideGroup(stack, e)
		}
	}
	if r.tooLarge {
		return Result{}, &Error{Code: ErrOutputTooLarge, Detail: "渲染结果总宽度超过 10^7"}
	}
	r.finishLine()
	return Result{Text: r.buf.String(), Overlong: r.overlong}, nil
}

// decideGroup 为断开上下文中遇到的组做平铺/断开决策，返回更新后的
// 工作清单。
func (r *renderer) decideGroup(stack []entry, e entry) []entry {
	if e.m == modeFlat {
		// 外层组已平铺：内部所有可断点都不断开，嵌套的组不再判定。
		r.cascadeOK = false
		return append(stack, entry{e.ind, modeFlat, e.d.child})
	}
	inf := r.info[e.d.child]
	// 含强制换行的组一律断开，不做放得下的判定。
	brk := inf.flat.stop
	if !brk && r.cascadeOK && r.col == r.cascadeCol {
		brk = true
	}
	if !brk {
		brk = !r.fits(stack, inf.flat)
	}
	if brk {
		r.cascadeOK = unwrapsToGroup(e.d.child, r.frags)
		r.cascadeCol = r.col
		return append(stack, entry{e.ind, modeBreak, e.d.child})
	}
	r.cascadeOK = false
	return append(stack, entry{e.ind, modeFlat, e.d.child})
}

// fits 放得下判定：组按平铺渲染（剖面 first）并连同其后续内容一起，
// 直到遇到第一个处于断开模式的可断点或强制换行（或文档结束）为止，
// 从当前列起累计宽度不超过行宽（取等视为放得下）。一旦累计宽度超过
// 剩余宽度立即返回 false。
func (r *renderer) fits(stack []entry, first profile) bool {
	rem := r.width - r.col
	if first.w > rem {
		return false
	}
	if first.stop {
		return true
	}
	rem -= first.w
	for i := len(stack) - 1; i >= 0; i-- {
		se := stack[i]
		var p profile
		if se.m == modeFlat {
			p = r.info[se.d].flat
		} else {
			p = r.info[se.d].brk
		}
		if p.w > rem {
			return false
		}
		if p.stop {
			return true
		}
		rem -= p.w
	}
	return true
}

// unwrapsToGroup 报告节点剥开缩进/对齐/引用后是否直接是一个组。
// 这些节点在放得下判定中都是透明的，且处理它们不产生任何输出，
// 因此级联断开是安全的。
func unwrapsToGroup(d *node, frags map[string]Doc) bool {
	for {
		switch d.kind {
		case kindIndent, kindAlign:
			d = d.child
		case kindRef:
			d = asNode(frags[d.name])
		case kindGroup:
			return true
		default:
			return false
		}
	}
}

// emit 输出一段文本（不含换行）。行首的缩进空格在首次输出非空内容时
// 才写入，因此只含缩进的行输出为空行。
func (r *renderer) emit(s string) {
	if s == "" {
		return
	}
	r.cascadeOK = false
	if r.pendingInd {
		r.writeSpaces(r.ind)
		r.pendingInd = false
	}
	r.buf.WriteString(s)
	w := Width(s)
	r.lineW += w
	r.col = r.lineW
	r.total += w
	r.updateTrail(s)
	r.checkSize()
}

// updateTrail 在写入 s 后更新当前行的行尾空格计数。
func (r *renderer) updateTrail(s string) {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		i--
	}
	if i == 0 {
		r.lineTrail += len(s)
	} else {
		r.lineTrail = len(s) - i
	}
}

// checkSize 在输出记账后检查输出上限。当前行末尾的空格最终会被裁掉，
// 不计入渲染结果的总宽度，因此检查时不计入。
func (r *renderer) checkSize() {
	if r.total-r.lineTrail > MaxOutputWidth {
		r.tooLarge = true
	}
}

// spaceChunk 是写入缩进空格时使用的分块。
const spaceChunk = "                                " // 32 个空格

// writeSpaces 写入 n 个空格并记账。
func (r *renderer) writeSpaces(n int) {
	left := n
	for left > 0 {
		chunk := left
		if chunk > len(spaceChunk) {
			chunk = len(spaceChunk)
		}
		r.buf.WriteString(spaceChunk[:chunk])
		left -= chunk
	}
	r.lineW += n
	r.total += n
	r.lineTrail += n
	r.checkSize()
}

// newline 结束当前行：去掉行尾空格、记录超宽行、写入换行符，并以
// 缩进量 ind 开始新行。
func (r *renderer) newline(ind int) {
	r.cascadeOK = false
	r.trimRight()
	if r.lineW > r.width {
		r.overlong = append(r.overlong, OverlongLine{Line: r.lineNo, Width: r.lineW})
	}
	r.buf.WriteByte('\n')
	r.total++
	r.lineNo++
	r.lineW = 0
	r.ind = ind
	r.col = ind
	r.pendingInd = true
	if r.total > MaxOutputWidth {
		r.tooLarge = true
	}
}

// trimRight 去掉当前行末尾的全部空格并同步记账。
func (r *renderer) trimRight() {
	if r.lineTrail > 0 {
		r.buf.Truncate(r.buf.Len() - r.lineTrail)
		r.lineW -= r.lineTrail
		r.total -= r.lineTrail
		r.lineTrail = 0
	}
}

// finishLine 收尾最后一行（不追加换行符）。
func (r *renderer) finishLine() {
	r.trimRight()
	if r.lineW > r.width {
		r.overlong = append(r.overlong, OverlongLine{Line: r.lineNo, Width: r.lineW})
	}
}
