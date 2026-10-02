// Package layout 实现缩进敏感语言的布局层状态机。
//
// 状态机逐物理行接收源码，按缩进栈、括号与续行、冒号块期望生成
// INDENT、DEDENT、NEWLINE 与 ENDMARKER 布局事件。
package layout

import (
	"strconv"
	"sync"
)

// MaxLineBytes 是单个物理行允许的最大字节数。
const MaxLineBytes = 10000

// 缩进深度与括号深度的合法取值范围。
const (
	MinIndentDepth  = 1
	MaxIndentDepth  = 100
	MinBracketDepth = 1
	MaxBracketDepth = 200
)

// Event 是布局事件。
type Event int

const (
	EventIndent Event = iota
	EventDedent
	EventNewline
	EventEndmarker
)

func (e Event) String() string {
	switch e {
	case EventIndent:
		return "INDENT"
	case EventDedent:
		return "DEDENT"
	case EventNewline:
		return "NEWLINE"
	case EventEndmarker:
		return "ENDMARKER"
	}
	return "UNKNOWN"
}

// Reason 是可区分的拒绝原因。
type Reason string

const (
	ReasonClosed           Reason = "closed"            // 已关闭
	ReasonLineTooLong      Reason = "line_too_long"     // 行过长
	ReasonInvalidParam     Reason = "invalid_param"     // 参数非法
	ReasonTabInconsistent  Reason = "tab_inconsistent"  // 制表符不一致
	ReasonIllegalDedent    Reason = "illegal_dedent"    // 非法缩减
	ReasonMissingIndent    Reason = "missing_indent"    // 缺少缩进
	ReasonUnexpectedIndent Reason = "unexpected_indent" // 意外缩进
	ReasonIndentTooDeep    Reason = "indent_too_deep"   // 缩进过深
	ReasonDanglingBranch   Reason = "dangling_branch"   // 悬垂分支
	ReasonStringError      Reason = "string_error"      // 字符串错误
	ReasonBracketError     Reason = "bracket_error"     // 括号错误
	ReasonBracketTooDeep   Reason = "bracket_too_deep"  // 括号过深
	ReasonInputIncomplete  Reason = "input_incomplete"  // 输入未完成（Close）
	ReasonMissingBlock     Reason = "missing_block"     // 缺少缩进块（Close）
)

// Error 是 Feed/Close 的拒绝结果，Reason 可精确区分原因。
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return "layout: " + string(e.Reason) + ": " + e.Detail
	}
	return "layout: " + string(e.Reason)
}

// indentEntry 是缩进栈项：c 为列宽，a 为备用列宽（空白字符数），
// h 为该层最近一个被接受的逻辑行起点的首词。
type indentEntry struct {
	c int
	a int
	h string
}

// Layout 是布局层状态机。零值不可用，须用 New 构造。
// 所有方法可并发调用，结果等价于某个串行顺序。
type Layout struct {
	mu sync.Mutex

	dm int // 缩进深度上限（不含栈底）
	bm int // 括号深度上限

	stack    []indentEntry // 缩进栈，栈底恒为 (0,0,"")
	brackets []byte        // 括号栈

	incomplete    bool // 逻辑行未完成（括号未闭或续行待续）
	colonExpected bool // 冒号块期望
	closed        bool // 已关闭

	lastSig    byte // 本逻辑行的最后有效字符
	hasLastSig bool

	scannedBytes int // 非导出计数器：被接受且被行内扫描的行的字节总数
}

// New 构造布局层状态机。dm 为缩进深度上限（1..100），bm 为括号深度
// 上限（1..200），参数越界返回 ReasonInvalidParam。
func New(dm, bm int) (*Layout, *Error) {
	if dm < MinIndentDepth || dm > MaxIndentDepth ||
		bm < MinBracketDepth || bm > MaxBracketDepth {
		return nil, &Error{Reason: ReasonInvalidParam}
	}
	return &Layout{
		dm:    dm,
		bm:    bm,
		stack: []indentEntry{{}},
	}, nil
}

// relation 是新逻辑行起点与当前栈顶的缩进关系。
type relation int

const (
	relSame   relation = iota // 同级
	relIndent                 // 缩进
	relDedent                 // 缩减 k 级
)

// Feed 接收一个不含换行符的物理行，返回该行产生的布局事件列表。
// 被拒绝的行视为从未发生：不改变任何状态。拒绝原因按"已关闭、
// 行过长、缩进类（仅新逻辑行起点，含悬垂分支）、词法"的次序只报
// 第一个。
func (l *Layout) Feed(line string) ([]Event, *Error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	if len(line) > MaxLineBytes {
		return nil, &Error{
			Reason: ReasonLineTooLong,
			Detail: strconv.Itoa(len(line)) + " > " + strconv.Itoa(MaxLineBytes),
		}
	}
	if l.incomplete {
		// 括号栈非空或上一被接受行以续行标记结束：延续行，
		// 不做缩进判定，只做行内扫描。
		return l.feedContinuation(line)
	}
	return l.feedLogicalStart(line)
}

// feedContinuation 处理延续行。
func (l *Layout) feedContinuation(line string) ([]Event, *Error) {
	scan := scanLine(line, l.brackets, l.bm)
	if scan.err != nil {
		return nil, scan.err
	}
	l.brackets = scan.brackets
	if scan.hasLastSig {
		l.lastSig = scan.lastSig
		l.hasLastSig = true
	}
	l.scannedBytes += len(line)
	if len(l.brackets) == 0 && !scan.continuation {
		return l.completeLogicalLine(nil), nil
	}
	l.incomplete = true
	return nil, nil
}

// feedLogicalStart 处理新逻辑行的起点。
func (l *Layout) feedLogicalStart(line string) ([]Event, *Error) {
	// 空白行与纯注释行不产生任何事件、不改变任何状态。
	if isBlankOrComment(line) {
		return nil, nil
	}
	c, a := indentWidth(line)
	top := l.stack[len(l.stack)-1]
	var rel relation
	var k int
	switch {
	case c == top.c:
		if a != top.a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
		rel = relSame
	case c > top.c:
		if a <= top.a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
		rel = relIndent
	default:
		// c < top.c：在栈的副本上弹栈直到栈顶 c 不大于当前 c
		//（栈底永不弹出）。
		idx := len(l.stack) - 1
		for idx > 0 && l.stack[idx].c > c {
			idx--
		}
		k = len(l.stack) - 1 - idx
		if l.stack[idx].c != c {
			return nil, &Error{Reason: ReasonIllegalDedent}
		}
		if l.stack[idx].a != a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
		rel = relDedent
	}
	if l.colonExpected && rel != relIndent {
		return nil, &Error{Reason: ReasonMissingIndent}
	}
	if !l.colonExpected && rel == relIndent {
		return nil, &Error{Reason: ReasonUnexpectedIndent}
	}
	if rel == relIndent && len(l.stack) > l.dm {
		return nil, &Error{Reason: ReasonIndentTooDeep}
	}
	// 悬垂分支：E 为关系生效后的栈顶项。
	w := firstWord(line)
	var eh string
	switch rel {
	case relSame:
		eh = l.stack[len(l.stack)-1].h
	case relIndent:
		eh = "" // 将压入的新项其 h 为空
	case relDedent:
		eh = l.stack[len(l.stack)-1-k].h
	}
	if !branchAllowed(w, eh) {
		return nil, &Error{Reason: ReasonDanglingBranch}
	}
	// 最后才是行内词法错误。
	scan := scanLine(line, l.brackets, l.bm)
	if scan.err != nil {
		return nil, scan.err
	}
	// 接受：先应用缩进关系并产生事件（先于该行的 NEWLINE）。
	var events []Event
	switch rel {
	case relIndent:
		l.stack = append(l.stack, indentEntry{c: c, a: a})
		events = append(events, EventIndent)
	case relDedent:
		l.stack = l.stack[:len(l.stack)-k]
		for i := 0; i < k; i++ {
			events = append(events, EventDedent)
		}
	}
	// 把生效后的栈顶项的 h 置为本行首词。
	l.stack[len(l.stack)-1].h = w
	// 新逻辑行起点处冒号块期望清为假、最后有效字符清空。
	l.colonExpected = false
	l.hasLastSig = false
	l.brackets = scan.brackets
	if scan.hasLastSig {
		l.lastSig = scan.lastSig
		l.hasLastSig = true
	}
	l.scannedBytes += len(line)
	if len(l.brackets) == 0 && !scan.continuation {
		return l.completeLogicalLine(events), nil
	}
	l.incomplete = true
	return events, nil
}

// completeLogicalLine 在逻辑行完成时产生 NEWLINE，并在本逻辑行
// 最后有效字符为冒号时把冒号块期望置真。
func (l *Layout) completeLogicalLine(events []Event) []Event {
	events = append(events, EventNewline)
	l.incomplete = false
	if l.hasLastSig && l.lastSig == ':' {
		l.colonExpected = true
	}
	return events
}

// Close 表示输入结束。已关闭、有未完成逻辑行、冒号块期望为真分别
// 拒绝；否则产生缩进栈中除栈底外每项一个 DEDENT，再产生 ENDMARKER。
func (l *Layout) Close() ([]Event, *Error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	if l.incomplete {
		return nil, &Error{Reason: ReasonInputIncomplete}
	}
	if l.colonExpected {
		return nil, &Error{Reason: ReasonMissingBlock}
	}
	var events []Event
	for len(l.stack) > 1 {
		l.stack = l.stack[:len(l.stack)-1]
		events = append(events, EventDedent)
	}
	events = append(events, EventEndmarker)
	l.closed = true
	return events, nil
}
