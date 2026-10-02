package layout

import "reflect"

// 本文件是测试专用的朴素模拟：每次 Feed 都从当前逻辑行的第一个
// 物理行起，把已接受的各行整体重扫来重建括号栈与标志，用于与
// 增量实现 Layout 做差分对照。它按规格逐条直写，不共享扫描代码。

type naiveLayout struct {
	dm, bm int

	stack []indentEntry

	logical []string // 当前逻辑行已接受的物理行（完成后保留至下一逻辑行起点）
	open    bool     // 逻辑行未完成

	colonExpected bool
	closed        bool

	// 以下状态由 rescan 从 logical 整体重建。
	brackets     []byte
	lastSig      byte
	hasLastSig   bool
	continuation bool
}

func newNaive(dm, bm int) *naiveLayout {
	return &naiveLayout{
		dm:    dm,
		bm:    bm,
		stack: []indentEntry{{}},
	}
}

// naiveScan 是朴素行内扫描：先定位字符串与注释，再处理括号与续行。
type naiveScan struct {
	brackets   []byte
	lastSig    byte
	hasLastSig bool
	cont       bool
	errReason  Reason
}

func naiveScanLine(line string, brackets []byte, bm int) naiveScan {
	bs := append([]byte(nil), brackets...)
	var last byte
	hasLast := false
	cont := false
	i := 0
	for i < len(line) {
		b := line[i]
		switch {
		case b == '#':
			i = len(line)
		case b == '\'' || b == '"':
			// 找到同种引号为止，反斜杠跳过其后一个字节。
			j := i + 1
			closed := false
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == b {
					closed = true
					break
				}
				j++
			}
			if !closed {
				return naiveScan{errReason: ReasonStringError}
			}
			last, hasLast = b, true // 闭合引号算有效字符
			i = j + 1
		case b == '(' || b == '[' || b == '{':
			if len(bs) >= bm {
				return naiveScan{errReason: ReasonBracketTooDeep}
			}
			bs = append(bs, b)
			last, hasLast = b, true
			i++
		case b == ')' || b == ']' || b == '}':
			if len(bs) == 0 || !bracketsMatch(bs[len(bs)-1], b) {
				return naiveScan{errReason: ReasonBracketError}
			}
			bs = bs[:len(bs)-1]
			last, hasLast = b, true
			i++
		case b == ' ' || b == '\t':
			i++
		case b == '\\' && i == len(line)-1:
			cont = true
			i++
		default:
			last, hasLast = b, true
			i++
		}
	}
	return naiveScan{brackets: bs, lastSig: last, hasLastSig: hasLast, cont: cont}
}

// rescan 从当前逻辑行的第一个物理行起整体重扫，重建括号栈、
// 最后有效字符与续行标志。
func (n *naiveLayout) rescan() {
	n.brackets = nil
	n.hasLastSig = false
	n.continuation = false
	for _, ln := range n.logical {
		r := naiveScanLine(ln, n.brackets, n.bm)
		if r.errReason != "" {
			panic("已接受的行重扫出错: " + ln)
		}
		n.brackets = r.brackets
		if r.hasLastSig {
			n.lastSig, n.hasLastSig = r.lastSig, true
		}
		n.continuation = r.cont
	}
}

func naiveBlankOrComment(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i == len(line) || line[i] == '#'
}

func naiveIndentWidth(line string) (c, a int) {
	i := 0
	for i < len(line) {
		if line[i] == ' ' {
			c, a = c+1, a+1
		} else if line[i] == '\t' {
			c = (c/8 + 1) * 8
			a++
		} else {
			break
		}
		i++
	}
	return c, a
}

func naiveFirstWord(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	end := i
	for end < len(line) && isWordByte(line[end]) {
		end++
	}
	return line[i:end]
}

func naiveBranchOK(w, eh string) bool {
	allowed := map[string][]string{
		"elif":    {"if", "elif"},
		"else":    {"if", "elif", "for", "while", "except"},
		"except":  {"try", "except"},
		"finally": {"try", "except", "else"},
	}
	set, ok := allowed[w]
	if !ok {
		return true
	}
	for _, h := range set {
		if eh == h {
			return true
		}
	}
	return false
}

func (n *naiveLayout) Feed(line string) ([]Event, *Error) {
	if n.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	if len(line) > MaxLineBytes {
		return nil, &Error{Reason: ReasonLineTooLong}
	}
	n.rescan()
	if len(n.brackets) > 0 || n.continuation {
		return n.feedContinuation(line)
	}
	return n.feedLogicalStart(line)
}

func (n *naiveLayout) feedContinuation(line string) ([]Event, *Error) {
	r := naiveScanLine(line, n.brackets, n.bm)
	if r.errReason != "" {
		return nil, &Error{Reason: r.errReason}
	}
	n.logical = append(n.logical, line)
	n.rescan()
	if len(n.brackets) == 0 && !n.continuation {
		n.open = false
		if n.hasLastSig && n.lastSig == ':' {
			n.colonExpected = true
		}
		return []Event{EventNewline}, nil
	}
	n.open = true
	return nil, nil
}

func (n *naiveLayout) feedLogicalStart(line string) ([]Event, *Error) {
	if naiveBlankOrComment(line) {
		return nil, nil
	}
	c, a := naiveIndentWidth(line)
	top := n.stack[len(n.stack)-1]
	var rel relation
	var k int
	switch {
	case c == top.c:
		if a != top.a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
	case c > top.c:
		if a <= top.a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
		rel = relIndent
	default:
		sim := append([]indentEntry(nil), n.stack...)
		for len(sim) > 1 && sim[len(sim)-1].c > c {
			sim = sim[:len(sim)-1]
			k++
		}
		if sim[len(sim)-1].c != c {
			return nil, &Error{Reason: ReasonIllegalDedent}
		}
		if sim[len(sim)-1].a != a {
			return nil, &Error{Reason: ReasonTabInconsistent}
		}
		rel = relDedent
	}
	if n.colonExpected && rel != relIndent {
		return nil, &Error{Reason: ReasonMissingIndent}
	}
	if !n.colonExpected && rel == relIndent {
		return nil, &Error{Reason: ReasonUnexpectedIndent}
	}
	if rel == relIndent && len(n.stack) > n.dm {
		return nil, &Error{Reason: ReasonIndentTooDeep}
	}
	w := naiveFirstWord(line)
	var eh string
	switch rel {
	case relIndent:
		eh = ""
	case relDedent:
		eh = n.stack[len(n.stack)-1-k].h
	default:
		eh = n.stack[len(n.stack)-1].h
	}
	if !naiveBranchOK(w, eh) {
		return nil, &Error{Reason: ReasonDanglingBranch}
	}
	r := naiveScanLine(line, nil, n.bm)
	if r.errReason != "" {
		return nil, &Error{Reason: r.errReason}
	}
	var events []Event
	switch rel {
	case relIndent:
		n.stack = append(n.stack, indentEntry{c: c, a: a})
		events = append(events, EventIndent)
	case relDedent:
		n.stack = n.stack[:len(n.stack)-k]
		for i := 0; i < k; i++ {
			events = append(events, EventDedent)
		}
	}
	n.stack[len(n.stack)-1].h = w
	n.colonExpected = false
	n.logical = []string{line}
	n.rescan()
	if len(n.brackets) == 0 && !n.continuation {
		n.open = false
		if n.hasLastSig && n.lastSig == ':' {
			n.colonExpected = true
		}
		events = append(events, EventNewline)
	} else {
		n.open = true
	}
	return events, nil
}

func (n *naiveLayout) Close() ([]Event, *Error) {
	if n.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	n.rescan()
	if n.open {
		return nil, &Error{Reason: ReasonInputIncomplete}
	}
	if n.colonExpected {
		return nil, &Error{Reason: ReasonMissingBlock}
	}
	var events []Event
	for len(n.stack) > 1 {
		n.stack = n.stack[:len(n.stack)-1]
		events = append(events, EventDedent)
	}
	events = append(events, EventEndmarker)
	n.closed = true
	return events, nil
}

// snapshot 导出与 Layout 相同形状的状态快照用于对照。
func (n *naiveLayout) snapshot() stateSnapshot {
	n.rescan()
	return stateSnapshot{
		stack:         append([]indentEntry(nil), n.stack...),
		brackets:      append([]byte(nil), n.brackets...),
		incomplete:    n.open,
		colonExpected: n.colonExpected,
		closed:        n.closed,
		lastSig:       n.lastSig,
		hasLastSig:    n.hasLastSig,
	}
}

// sameState 比较两个状态快照（不计 scannedBytes 计数器）。
func sameState(a, b stateSnapshot) bool {
	a.scannedBytes = 0
	b.scannedBytes = 0
	return reflect.DeepEqual(a, b)
}
