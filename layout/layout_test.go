package layout

import (
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, dm, bm int) *Layout {
	t.Helper()
	l, err := New(dm, bm)
	if err != nil {
		t.Fatalf("New(%d, %d) 被拒绝: %v", dm, bm, err)
	}
	return l
}

// feed 期望该行被接受，返回事件列表。
func feed(t *testing.T, l *Layout, line string) []Event {
	t.Helper()
	ev, err := l.Feed(line)
	if err != nil {
		t.Fatalf("Feed(%q) 被意外拒绝: %v", line, err)
	}
	return ev
}

// expectEvents 断言该行被接受且产生的事件序列完全相等。
func expectEvents(t *testing.T, l *Layout, line string, want ...Event) {
	t.Helper()
	ev := feed(t, l, line)
	if !eventsEqual(ev, want) {
		t.Fatalf("Feed(%q) 事件 = %v, 期望 %v", line, ev, want)
	}
}

// expectReject 断言该行被拒绝且原因精确匹配。
func expectReject(t *testing.T, l *Layout, line string, want Reason) {
	t.Helper()
	ev, err := l.Feed(line)
	if err == nil {
		t.Fatalf("Feed(%q) 被意外接受, 事件 = %v, 期望拒绝 %v", line, ev, want)
	}
	if err.Reason != want {
		t.Fatalf("Feed(%q) 拒绝原因 = %v, 期望 %v", line, err.Reason, want)
	}
	if ev != nil {
		t.Fatalf("Feed(%q) 被拒绝却返回事件 %v", line, ev)
	}
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stateSnapshot 捕获全部内部状态，用于验证"被拒绝的行不改任何状态"
// 与重放一致性。
type stateSnapshot struct {
	stack         []indentEntry
	brackets      []byte
	incomplete    bool
	colonExpected bool
	closed        bool
	lastSig       byte
	hasLastSig    bool
	scannedBytes  int
}

func snap(l *Layout) stateSnapshot {
	return stateSnapshot{
		stack:         append([]indentEntry(nil), l.stack...),
		brackets:      append([]byte(nil), l.brackets...),
		incomplete:    l.incomplete,
		colonExpected: l.colonExpected,
		closed:        l.closed,
		lastSig:       l.lastSig,
		hasLastSig:    l.hasLastSig,
		scannedBytes:  l.scannedBytes,
	}
}

func expectStateUnchanged(t *testing.T, before, after stateSnapshot, what string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s 后状态被改变:\n之前: %+v\n之后: %+v", what, before, after)
	}
}

// expectRejectKeepsState 断言拒绝且状态完全不变。
func expectRejectKeepsState(t *testing.T, l *Layout, line string, want Reason) {
	t.Helper()
	before := snap(l)
	expectReject(t, l, line, want)
	expectStateUnchanged(t, before, snap(l), "拒绝 "+string(want))
}

func closeExpect(t *testing.T, l *Layout, want ...Event) {
	t.Helper()
	ev, err := l.Close()
	if err != nil {
		t.Fatalf("Close() 被意外拒绝: %v", err)
	}
	if !eventsEqual(ev, want) {
		t.Fatalf("Close() 事件 = %v, 期望 %v", ev, want)
	}
}

func closeReject(t *testing.T, l *Layout, want Reason) {
	t.Helper()
	ev, err := l.Close()
	if err == nil {
		t.Fatalf("Close() 被意外接受, 事件 = %v, 期望拒绝 %v", ev, want)
	}
	if err.Reason != want {
		t.Fatalf("Close() 拒绝原因 = %v, 期望 %v", err.Reason, want)
	}
}

// TestSpecExample 是题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "    foo(1,", EventIndent)
	expectEvents(t, l, "  2)", EventNewline)
	expectEvents(t, l, "    # c")
	expectRejectKeepsState(t, l, "  bar", ReasonIllegalDedent)
	expectEvents(t, l, "bar", EventDedent, EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestMissingIndentRecovery 对应：期望为真时同级被拒、期望保持、
// 随后缩进被接受。
func TestMissingIndentRecovery(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectRejectKeepsState(t, l, "y", ReasonMissingIndent)
	if !l.colonExpected {
		t.Fatal("缺少缩进被拒后冒号块期望应保持为真")
	}
	expectEvents(t, l, "  y", EventIndent, EventNewline)
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// TestUnexpectedIndent 对应：期望为假时缩进被拒。
func TestUnexpectedIndent(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "y", EventNewline)
	expectRejectKeepsState(t, l, "  z", ReasonUnexpectedIndent)
	expectEvents(t, l, "z", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestExpectedDedentRelation 期望为真时缩减关系同样被拒为缺少缩进。
func TestExpectedDedentRelation(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if a:", EventNewline)
	expectEvents(t, l, "  if b:", EventIndent, EventNewline)
	expectEvents(t, l, "    x:", EventIndent, EventNewline)
	expectRejectKeepsState(t, l, "if c:", ReasonMissingIndent)
	expectRejectKeepsState(t, l, "    y", ReasonMissingIndent)
	expectEvents(t, l, "      y", EventIndent, EventNewline)
}

// TestTabInconsistentSameWidth c 相同而 a 不同（两个方向）。
func TestTabInconsistentSameWidth(t *testing.T) {
	// 栈为 [(0,0),(8,8)]（八个空格）时 "\tx" 的 a=1 不等于 8。
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "        y", EventIndent, EventNewline)
	expectRejectKeepsState(t, l, "\tx", ReasonTabInconsistent)
	// 栈为 [(0,0),(8,1)]（一个制表符）时八个空格同样被拒。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "if x:", EventNewline)
	expectEvents(t, l2, "\ty", EventIndent, EventNewline)
	expectRejectKeepsState(t, l2, "        x", ReasonTabInconsistent)
}

// TestTabInconsistentDeeper c 更大而 a 不更大。
func TestTabInconsistentDeeper(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "        y", EventIndent, EventNewline) // (8,8)
	expectRejectKeepsState(t, l, "\t\tz", ReasonTabInconsistent)
}

// TestTabInconsistentDedent 缩减到某层时 a 必须等于该层 a。
func TestTabInconsistentDedent(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "\t\tif y:", EventIndent, EventNewline) // (16,2)
	expectEvents(t, l, "\t\t\tz", EventIndent, EventNewline)   // (24,3)
	// 16 个空格：c=16 等于 (16,2) 的 c，但 a=16 不等于 2。
	expectRejectKeepsState(t, l, "                v", ReasonTabInconsistent)
	// 缩减到 (16,2) 且 a 一致则接受。
	expectEvents(t, l, "\t\tv", EventDedent, EventNewline)
}

// TestTabStops 制表符增至下一个 8 的倍数，含已在 8 的倍数处再遇
// 制表符（8 个空格 + 制表符得到 c=16, a=9）。
func TestTabStops(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "        \ty", EventIndent, EventNewline) // (16,9)
	expectEvents(t, l, "        \tz", EventNewline)              // 恰同级
	// "\t\t" 是 (16,2)，c 相同而 a 不同。
	expectRejectKeepsState(t, l, "\t\tw", ReasonTabInconsistent)
	// 反向：(16,2) 在栈上时 "        \t" 同样不一致。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "if x:", EventNewline)
	expectEvents(t, l2, "\t\ty", EventIndent, EventNewline) // (16,2)
	expectRejectKeepsState(t, l2, "        \tz", ReasonTabInconsistent)
	// 混合：空格到 7 再遇制表符进位到 8。
	l3 := mustNew(t, 10, 10)
	expectEvents(t, l3, "if x:", EventNewline)
	expectEvents(t, l3, "       \ty", EventIndent, EventNewline) // (8,8)
	expectEvents(t, l3, "        z", EventNewline)               // (8,8) 恰同级
}

// TestBlankAndCommentLines 空白行与纯注释行不产生事件、不改变状态、
// 不清除冒号块期望。
func TestBlankAndCommentLines(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	for _, blank := range []string{"", "   ", "\t\t", "  # comment", "#top", " \t # x"} {
		before := snap(l)
		expectEvents(t, l, blank)
		expectStateUnchanged(t, before, snap(l), "空白/注释行 "+blank)
	}
	expectRejectKeepsState(t, l, "y", ReasonMissingIndent)
	expectEvents(t, l, "  y", EventIndent, EventNewline)
	expectEvents(t, l, "")
	expectEvents(t, l, "   # c")
	expectEvents(t, l, "  z", EventNewline) // 同级不受空白行影响
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// TestColonExpectationTiming 冒号只在逻辑行末（含注释前与续行后）
// 才置期望。
func TestColonExpectationTiming(t *testing.T) {
	// 注释前的冒号置期望。
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x: # c", EventNewline)
	expectRejectKeepsState(t, l, "y", ReasonMissingIndent)
	// 字符串内的冒号不置期望（最后有效字符是闭合引号）。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "d = ':'", EventNewline)
	expectEvents(t, l2, "e = 1", EventNewline)
	// 冒号不在逻辑行末不置期望。
	l3 := mustNew(t, 10, 10)
	expectEvents(t, l3, "if x: y", EventNewline)
	expectEvents(t, l3, "z", EventNewline)
	// 续行之后冒号仍是逻辑行最后有效字符。
	l4 := mustNew(t, 10, 10)
	expectEvents(t, l4, "if x: \\")
	expectEvents(t, l4, ":", EventNewline)
	expectRejectKeepsState(t, l4, "y", ReasonMissingIndent)
	// 续行之后最后有效字符不是冒号则不置期望。
	l5 := mustNew(t, 10, 10)
	expectEvents(t, l5, "if x: \\")
	expectEvents(t, l5, "y", EventNewline)
	expectEvents(t, l5, "z", EventNewline)
	// 续行前最后有效字符不是冒号、续行后才是。
	l6 := mustNew(t, 10, 10)
	expectEvents(t, l6, "x = 1 + \\")
	expectEvents(t, l6, ":", EventNewline)
	expectRejectKeepsState(t, l6, "y", ReasonMissingIndent)
}

// TestStringsCommentsHideSyntax 引号内与注释内的括号、冒号与反斜杠
// 不起作用。
func TestStringsCommentsHideSyntax(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = '(' # )", EventNewline)
	expectEvents(t, l, "y = ')'", EventNewline)
	expectEvents(t, l, `z = "#]"`, EventNewline)
	expectEvents(t, l, "w = 1 # ([{", EventNewline)
	expectEvents(t, l, "v = '\\\\'", EventNewline) // 字符串内是转义的反斜杠
	expectEvents(t, l, "u = '\\''", EventNewline)  // 字符串内是转义的引号
	expectEvents(t, l, "a = 1 # '", EventNewline)  // 注释内的引号不是字符串
	expectEvents(t, l, "b = 1 # \\", EventNewline) // 注释内的反斜杠不是续行
	expectEvents(t, l, "c = 2", EventNewline)      // 上一行未续行
	// 字符串内的冒号不置期望，闭合引号是最后有效字符。
	expectEvents(t, l, "d = ':'", EventNewline)
	expectEvents(t, l, "e = 3", EventNewline)
	// 字符串内的括号不进括号栈。
	expectEvents(t, l, "f = '([{'", EventNewline)
	expectEvents(t, l, "g = 4", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestStringErrors 行内未闭合的字符串、引号内最后一字节是反斜杠。
func TestStringErrors(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = 1", EventNewline)
	expectRejectKeepsState(t, l, "s = 'abc", ReasonStringError)
	expectRejectKeepsState(t, l, `s = "abc`, ReasonStringError)
	expectRejectKeepsState(t, l, "s = 'ab\\", ReasonStringError)   // 引号内末字节是反斜杠
	expectRejectKeepsState(t, l, "s = 'abc\\'", ReasonStringError) // 末字节引号被转义
	expectRejectKeepsState(t, l, "s = \"a'b", ReasonStringError)   // 异种引号不闭合
	// 行内从左到右的第一个词法错误为准：右括号先于未闭合字符串。
	expectRejectKeepsState(t, l, "x = ) 'abc", ReasonBracketError)
	expectEvents(t, l, "s = 'ok'", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestBracketsSuspendIndent 括号内的行不判缩进；括号内的空行与
// 注释行被扫描但不产生缩进事件。
func TestBracketsSuspendIndent(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = (")
	expectEvents(t, l, "     1,")
	expectEvents(t, l, "2)", EventNewline)
	// 括号内的空行与纯注释行。
	expectEvents(t, l, "y = [")
	expectEvents(t, l, "")
	expectEvents(t, l, "   # c")
	expectEvents(t, l, "\t\t")
	expectEvents(t, l, "1]", EventNewline)
	// 括号内的行即使缩进列宽非法也不判缩进。
	expectEvents(t, l, "if x:", EventNewline)
	expectEvents(t, l, "  y = (", EventIndent)
	expectEvents(t, l, "1)", EventNewline) // c=1 若判缩进将是非法缩减
	expectEvents(t, l, "  z", EventNewline)
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// TestBracketErrors 括号不匹配与栈空时的右括号。
func TestBracketErrors(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectRejectKeepsState(t, l, ")", ReasonBracketError)
	expectRejectKeepsState(t, l, "x = )", ReasonBracketError)
	expectRejectKeepsState(t, l, "(]", ReasonBracketError)
	expectRejectKeepsState(t, l, "x = [}", ReasonBracketError)
	expectRejectKeepsState(t, l, "{)", ReasonBracketError)
	expectRejectKeepsState(t, l, "([)]", ReasonBracketError)
	expectEvents(t, l, "y", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestBracketDepthLimit 括号深度上限恰好允许、超一被拒。
func TestBracketDepthLimit(t *testing.T) {
	l := mustNew(t, 10, 3)
	expectEvents(t, l, "x = (((") // 深度 3 恰好允许
	expectRejectKeepsState(t, l, "[", ReasonBracketTooDeep)
	expectEvents(t, l, ")))", EventNewline)
	expectRejectKeepsState(t, l, "y = [[[(", ReasonBracketTooDeep)
	expectEvents(t, l, "y = [[[") // 上一行被拒，未留下括号
	closeReject(t, l, ReasonInputIncomplete)
	expectEvents(t, l, "]]]", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestIndentDepthLimit 缩进深度上限恰好允许、超一被拒。
func TestIndentDepthLimit(t *testing.T) {
	l := mustNew(t, 2, 10)
	expectEvents(t, l, "if a:", EventNewline)
	expectEvents(t, l, "  if b:", EventIndent, EventNewline)
	expectEvents(t, l, "    if c:", EventIndent, EventNewline) // 深度 2 恰好允许
	// 期望为真时再缩进一级：超过 Dm=2。
	expectRejectKeepsState(t, l, "      x", ReasonIndentTooDeep)
	// 状态未变：期望仍为真，同级仍是缺少缩进。
	expectRejectKeepsState(t, l, "    y", ReasonMissingIndent)
	closeReject(t, l, ReasonMissingBlock)
	// Dm=1 的情形。
	l2 := mustNew(t, 1, 10)
	expectEvents(t, l2, "if a:", EventNewline)
	expectEvents(t, l2, "  x", EventIndent, EventNewline) // 深度 1 恰好允许
	expectEvents(t, l2, "if b:", EventDedent, EventNewline)
	expectEvents(t, l2, "  if c:", EventIndent, EventNewline)
	expectRejectKeepsState(t, l2, "    y", ReasonIndentTooDeep)
}

// TestContinuation 续行规则：标记后接任何字节都不算续行；续行后接
// 空白行使逻辑行完成。
func TestContinuation(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = 1 + \\")
	expectEvents(t, l, "      2", EventNewline)
	// 续行标记后接空格不算续行。
	expectEvents(t, l, "y = 1 + \\ ", EventNewline)
	expectEvents(t, l, "z = 2", EventNewline) // 上一行已完成
	// 续行后接空白行使逻辑行完成。
	expectEvents(t, l, "a = 1 + \\")
	expectEvents(t, l, "", EventNewline)
	expectEvents(t, l, "b = 1 + \\")
	expectEvents(t, l, "   ", EventNewline)
	expectEvents(t, l, "c = 3", EventNewline)
	// 续行标记在括号未闭时同样成立。
	expectEvents(t, l, "d = (1 + \\")
	expectEvents(t, l, "2)", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestContinuationThenComment 续行后接纯注释行也使逻辑行完成
// （延续行只做行内扫描，注释被忽略）。
func TestContinuationThenComment(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = 1 + \\")
	expectEvents(t, l, "# done", EventNewline)
	expectEvents(t, l, "y = 2", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestErrorPrecedence 拒绝次序：缩进类（含悬垂分支）先于词法错误；
// 悬垂分支后于缩进过深。
func TestErrorPrecedence(t *testing.T) {
	// 缺少缩进先于字符串错误。
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if x:", EventNewline)
	expectRejectKeepsState(t, l, "y = 'abc", ReasonMissingIndent)
	// 意外缩进先于字符串错误。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "x", EventNewline)
	expectRejectKeepsState(t, l2, "  y = 'abc", ReasonUnexpectedIndent)
	// 悬垂分支后于缩进过深、先于词法错误。
	l3 := mustNew(t, 1, 10)
	expectEvents(t, l3, "if x:", EventNewline)
	expectEvents(t, l3, "  if y:", EventIndent, EventNewline)
	// 该行同时是缩进过深、悬垂分支（新项 h 为空）与字符串错误。
	expectRejectKeepsState(t, l3, "    else: '", ReasonIndentTooDeep)
	// 悬垂分支先于字符串错误。
	l4 := mustNew(t, 10, 10)
	expectEvents(t, l4, "y = 1", EventNewline)
	expectRejectKeepsState(t, l4, "else: '", ReasonDanglingBranch)
	// 非法缩减先于词法错误。
	l5 := mustNew(t, 10, 10)
	expectEvents(t, l5, "if x:", EventNewline)
	expectEvents(t, l5, "    y", EventIndent, EventNewline)
	expectRejectKeepsState(t, l5, "  z = '", ReasonIllegalDedent)
}

// TestDanglingKeepsHeadWord 悬垂分支被拒后栈顶 h 不变。
func TestDanglingKeepsHeadWord(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "y = 1", EventNewline)
	expectRejectKeepsState(t, l, "else:", ReasonDanglingBranch)
	expectRejectKeepsState(t, l, "else:", ReasonDanglingBranch) // h 仍为 y
	expectRejectKeepsState(t, l, "elif x:", ReasonDanglingBranch)
	expectEvents(t, l, "if x:", EventNewline) // h 变为 if
	expectEvents(t, l, "  a", EventIndent, EventNewline)
	expectEvents(t, l, "else:", EventDedent, EventNewline) // if 允许 else
	expectEvents(t, l, "  b", EventIndent, EventNewline)
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// tryBranch 用 setup 行构造布局（使缩减后栈顶 h 为指定首词），
// 再在 0 级喂 branch 行，返回其拒绝结果（nil 表示接受）。
func tryBranch(t *testing.T, setup []string, branch string) *Error {
	t.Helper()
	l := mustNew(t, 10, 10)
	for _, s := range setup {
		feed(t, l, s)
	}
	_, err := l.Feed(branch)
	return err
}

// TestDanglingAllowSets 各分支关键字的允许集合（含 finally 接在
// else 之后）。
func TestDanglingAllowSets(t *testing.T) {
	// 每个 setup 的最后一行缩进一级，使分支行在 0 级缩减到栈底，
	// 此时栈底 h 为给定首词。
	setups := map[string][]string{
		"if":      {"if x:", "  a"},
		"elif":    {"if x:", "  a", "elif y:", "  b"},
		"else":    {"if x:", "  a", "else:", "  b"},
		"for":     {"for i:", "  a"},
		"while":   {"while x:", "  a"},
		"try":     {"try:", "  a"},
		"except":  {"try:", "  a", "except:", "  b"},
		"finally": {"try:", "  a", "finally:", "  b"},
		"y":       {"y = 1"},
	}
	allow := map[string][]string{
		"elif":    {"if", "elif"},
		"else":    {"if", "elif", "for", "while", "except"},
		"except":  {"try", "except"},
		"finally": {"try", "except", "else"},
	}
	branchLine := map[string]string{
		"elif":    "elif z:",
		"else":    "else:",
		"except":  "except:",
		"finally": "finally:",
	}
	for branch, heads := range allow {
		for head, setup := range setups {
			err := tryBranch(t, setup, branchLine[branch])
			wantAccept := false
			for _, h := range heads {
				if h == head {
					wantAccept = true
				}
			}
			if wantAccept && err != nil {
				t.Fatalf("h=%q 应允许 %q, 却被拒: %v", head, branch, err)
			}
			if !wantAccept {
				if err == nil {
					t.Fatalf("h=%q 不应允许 %q, 却被接受", head, branch)
				}
				if err.Reason != ReasonDanglingBranch {
					t.Fatalf("h=%q 喂 %q 的拒绝原因 = %v, 期望 %v",
						head, branch, err.Reason, ReasonDanglingBranch)
				}
			}
		}
	}
}

// TestDanglingWholeWord 首词整串比较：elsewhere、else_ 等不算 else。
func TestDanglingWholeWord(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "y = 1", EventNewline)
	expectEvents(t, l, "elsewhere = 1", EventNewline)
	expectEvents(t, l, "else_ = 1", EventNewline)
	expectEvents(t, l, "else2 = 1", EventNewline)
	expectEvents(t, l, "_else = 1", EventNewline)
	expectEvents(t, l, "elif_ = 1", EventNewline)
	expectEvents(t, l, "finally2 = 1", EventNewline)
	// 栈顶 h 现在是 finally2，不是允许集合成员。
	expectRejectKeepsState(t, l, "else:", ReasonDanglingBranch)
	// 引号开头的行首词为空，不算分支关键字。
	expectEvents(t, l, "'else': 1", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestDedentUsesOuterHeadWord 缩减到更外层时用的是该层自己的 h。
func TestDedentUsesOuterHeadWord(t *testing.T) {
	// 外层 h 为 for，允许 else；若误用内层 h=try 将被拒。
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "for i:", EventNewline)
	expectEvents(t, l, "  try:", EventIndent, EventNewline)
	expectEvents(t, l, "    x", EventIndent, EventNewline)
	expectEvents(t, l, "else:", EventDedent, EventDedent, EventNewline)
	expectEvents(t, l, "  y", EventIndent, EventNewline)
	closeExpect(t, l, EventDedent, EventEndmarker)
	// 外层 h 为 try，不允许 else；若误用内层 h=if 将被接受。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "try:", EventNewline)
	expectEvents(t, l2, "  if b:", EventIndent, EventNewline)
	expectEvents(t, l2, "    x", EventIndent, EventNewline)
	expectRejectKeepsState(t, l2, "else:", ReasonDanglingBranch)
	expectEvents(t, l2, "except:", EventDedent, EventDedent, EventNewline)
	expectEvents(t, l2, "  y", EventIndent, EventNewline)
	closeExpect(t, l2, EventDedent, EventEndmarker)
}

// TestMultiLevelDedent 一次缩减多级产生对应个数的 DEDENT；缩减到
// 栈中不存在的列宽为非法缩减。
func TestMultiLevelDedent(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "if a:", EventNewline)
	expectEvents(t, l, "  if b:", EventIndent, EventNewline)
	expectEvents(t, l, "    if c:", EventIndent, EventNewline)
	expectEvents(t, l, "      x", EventIndent, EventNewline)
	// 一次缩减三级。
	expectEvents(t, l, "y", EventDedent, EventDedent, EventDedent, EventNewline)
	// 缩减到中间层恰好一级。
	expectEvents(t, l, "if d:", EventNewline)
	expectEvents(t, l, "  if e:", EventIndent, EventNewline)
	expectEvents(t, l, "    x", EventIndent, EventNewline)
	expectEvents(t, l, "  y", EventDedent, EventNewline)
	expectEvents(t, l, "z", EventDedent, EventNewline)
	// 缩减到栈中不存在的列宽。
	expectEvents(t, l, "if f:", EventNewline)
	expectEvents(t, l, "      g", EventIndent, EventNewline)
	expectRejectKeepsState(t, l, "  h", ReasonIllegalDedent)
	expectRejectKeepsState(t, l, "    h", ReasonIllegalDedent)
	expectEvents(t, l, "      i", EventNewline) // 恰同级不受影响
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// TestSameLevel 缩进恰同级无事件。
func TestSameLevel(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x", EventNewline)
	expectEvents(t, l, "y", EventNewline)
	expectEvents(t, l, "if a:", EventNewline)
	expectEvents(t, l, "  b", EventIndent, EventNewline)
	expectEvents(t, l, "  c", EventNewline)
	expectEvents(t, l, "  d", EventNewline)
	closeExpect(t, l, EventDedent, EventEndmarker)
}

// TestCloseRejects Close 的三种拒绝与先后。
func TestCloseRejects(t *testing.T) {
	// 输入未完成：括号未闭。
	l := mustNew(t, 10, 10)
	expectEvents(t, l, "x = (")
	closeReject(t, l, ReasonInputIncomplete)
	expectEvents(t, l, "1)", EventNewline) // 拒绝不关闭，可继续喂
	closeExpect(t, l, EventEndmarker)
	// 输入未完成：续行待续。
	l2 := mustNew(t, 10, 10)
	expectEvents(t, l2, "x = \\")
	closeReject(t, l2, ReasonInputIncomplete)
	expectEvents(t, l2, "1", EventNewline)
	closeExpect(t, l2, EventEndmarker)
	// 缺少缩进块：冒号块期望为真。
	l3 := mustNew(t, 10, 10)
	expectEvents(t, l3, "if x:", EventNewline)
	closeReject(t, l3, ReasonMissingBlock)
	expectEvents(t, l3, "  y", EventIndent, EventNewline)
	closeExpect(t, l3, EventDedent, EventEndmarker)
	// 已关闭：Feed 与 Close 都拒绝；已关闭先于其他原因。
	expectRejectKeepsState(t, l3, "x", ReasonClosed)
	closeReject(t, l3, ReasonClosed)
	expectRejectKeepsState(t, l3, strings.Repeat("x", MaxLineBytes+1), ReasonClosed)
	// 空输入直接关闭。
	l4 := mustNew(t, 10, 10)
	closeExpect(t, l4, EventEndmarker)
	closeReject(t, l4, ReasonClosed)
}

// TestCloseDedentCount Close 产生除栈底外每项一个 DEDENT，
// 累计 DEDENT 数恰等于累计 INDENT 数。
func TestCloseDedentCount(t *testing.T) {
	l := mustNew(t, 10, 10)
	indents, dedents := 0, 0
	count := func(ev []Event) {
		for _, e := range ev {
			switch e {
			case EventIndent:
				indents++
			case EventDedent:
				dedents++
			}
			if dedents > indents {
				t.Fatal("累计 DEDENT 数超过累计 INDENT 数")
			}
		}
	}
	for _, line := range []string{
		"if a:", "  if b:", "    if c:", "      x", "    y", "  z", "w",
	} {
		ev := feed(t, l, line)
		count(ev)
	}
	ev, err := l.Close()
	if err != nil {
		t.Fatalf("Close() 被拒: %v", err)
	}
	count(ev)
	if indents != dedents {
		t.Fatalf("Close 后 INDENT=%d 与 DEDENT=%d 不相等", indents, dedents)
	}
	if indents != 3 {
		t.Fatalf("INDENT 总数 = %d, 期望 3", indents)
	}
}

// TestConstructor 构造参数合法性。
func TestConstructor(t *testing.T) {
	for _, p := range [][2]int{{0, 1}, {1, 0}, {101, 1}, {1, 201}, {-1, 5}, {5, -3}, {0, 0}, {200, 300}} {
		if _, err := New(p[0], p[1]); err == nil {
			t.Fatalf("New(%d, %d) 应被拒绝", p[0], p[1])
		} else if err.Reason != ReasonInvalidParam {
			t.Fatalf("New(%d, %d) 原因 = %v, 期望 %v", p[0], p[1], err.Reason, ReasonInvalidParam)
		}
	}
	for _, p := range [][2]int{{1, 1}, {100, 200}, {1, 200}, {100, 1}, {50, 60}} {
		if _, err := New(p[0], p[1]); err != nil {
			t.Fatalf("New(%d, %d) 应被接受, 却被拒: %v", p[0], p[1], err)
		}
	}
}

// TestLineTooLong 行至多 10^4 字节；行过长先于其他判定。
func TestLineTooLong(t *testing.T) {
	l := mustNew(t, 10, 10)
	expectEvents(t, l, strings.Repeat("x", MaxLineBytes), EventNewline)
	expectRejectKeepsState(t, l, strings.Repeat("x", MaxLineBytes+1), ReasonLineTooLong)
	// 过长的纯注释行同样被拒（行过长先于空白行判定）。
	expectRejectKeepsState(t, l, "#"+strings.Repeat("c", MaxLineBytes), ReasonLineTooLong)
	// 括号内（延续行）过长同样被拒。
	expectEvents(t, l, "y = (")
	expectRejectKeepsState(t, l, strings.Repeat("1", MaxLineBytes+1), ReasonLineTooLong)
	expectEvents(t, l, "1)", EventNewline)
	closeExpect(t, l, EventEndmarker)
}

// TestScannedBytesCounter 非导出计数器：被扫描的字节总数等于被接受
// 且被扫描行的字节总数；延续行不重扫此前的行。
func TestScannedBytesCounter(t *testing.T) {
	l := mustNew(t, 10, 10)
	steps := []struct {
		line    string
		scanned bool // 该行是否会被行内扫描
		reject  bool
	}{
		{"if x:", true, false},
		{"  # c", false, false}, // 纯注释行不扫描
		{"", false, false},      // 空白行不扫描
		{"  foo(1,", true, false},
		{"  2)", true, false}, // 延续行只扫描本行
		{"", false, false},
		{"x = (", true, false},
		{"", true, false},      // 括号内的空白行被扫描（0 字节）
		{"  # c", true, false}, // 括号内的注释行被扫描
		{"1)", true, false},
		{"bad )", true, true}, // 词法错误被拒，不计数
		{"  bad", true, true}, // 缩进类错误被拒，不计数
		{"y", true, false},
	}
	want := 0
	for _, s := range steps {
		_, err := l.Feed(s.line)
		if s.reject && err == nil {
			t.Fatalf("Feed(%q) 应被拒绝", s.line)
		}
		if !s.reject && err != nil {
			t.Fatalf("Feed(%q) 应被接受, 却被拒: %v", s.line, err)
		}
		if s.scanned && !s.reject {
			want += len(s.line)
		}
	}
	if l.scannedBytes != want {
		t.Fatalf("scannedBytes = %d, 期望 %d", l.scannedBytes, want)
	}
}

// TestReplay 相同的行序列重放得到完全相同的事件序列、错误与状态。
func TestReplay(t *testing.T) {
	lines := []string{
		"if x:", "  y = (", "1,", "  2)", "# c", "else:", "  z = 'a\\'b'",
		"  w = 1 + \\", "  2", "try:", "  a", "except:", "  b", "finally:",
		"  c", "bad )", "  q", "end",
	}
	run := func() ([][]Event, []Reason, stateSnapshot) {
		l := mustNew(t, 10, 10)
		var events [][]Event
		var reasons []Reason
		for _, line := range lines {
			ev, err := l.Feed(line)
			events = append(events, ev)
			var r Reason
			if err != nil {
				r = err.Reason
			}
			reasons = append(reasons, r)
		}
		ev, _ := l.Close()
		events = append(events, ev)
		return events, reasons, snap(l)
	}
	e1, r1, s1 := run()
	e2, r2, s2 := run()
	if !reflect.DeepEqual(e1, e2) {
		t.Fatalf("重放事件序列不一致:\n%v\n%v", e1, e2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("重放错误序列不一致:\n%v\n%v", r1, r2)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("重放状态不一致:\n%+v\n%+v", s1, s2)
	}
}

// TestFirstWord 首词：去掉前导空白后首个词法字符最长连续串。
func TestFirstWord(t *testing.T) {
	cases := map[string]string{
		"if x:":      "if",
		"  elif y:":  "elif",
		"\telse:":    "else",
		"elsewhere:": "elsewhere",
		"else_:":     "else_",
		"9lives":     "9lives",
		"_x 1":       "_x",
		"(x":         "",
		"'s'":        "",
		"":           "",
		"   ":        "",
		"x-y":        "x",
	}
	for line, want := range cases {
		if got := firstWord(line); got != want {
			t.Fatalf("firstWord(%q) = %q, 期望 %q", line, got, want)
		}
	}
}

// TestConcurrent 并发调用等价于某个串行顺序：8 个 goroutine 各喂
// 100 行同级普通行，每行恰好产生一个 NEWLINE，与任何串行顺序一致。
func TestConcurrent(t *testing.T) {
	l := mustNew(t, 10, 10)
	var newlines int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ev, err := l.Feed("x = 1")
				if err != nil {
					t.Errorf("Feed 被拒: %v", err)
					return
				}
				for _, e := range ev {
					if e == EventNewline {
						atomic.AddInt64(&newlines, 1)
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&newlines); got != 800 {
		t.Fatalf("NEWLINE 总数 = %d, 期望 800", got)
	}
	closeExpect(t, l, EventEndmarker)
	expectReject(t, l, "x = 1", ReasonClosed)
}

// TestErrorString 拒绝原因可区分且错误文本包含原因。
func TestErrorString(t *testing.T) {
	l := mustNew(t, 10, 10)
	_, err := l.Feed(")")
	if err == nil || err.Reason != ReasonBracketError {
		t.Fatalf("期望括号错误, 得到 %v", err)
	}
	if !strings.Contains(err.Error(), string(ReasonBracketError)) {
		t.Fatalf("错误文本 %q 不含原因 %q", err.Error(), ReasonBracketError)
	}
	detailed := &Error{Reason: ReasonLineTooLong, Detail: "10001 > 10000"}
	if !strings.Contains(detailed.Error(), "10001 > 10000") {
		t.Fatalf("错误文本 %q 不含细节", detailed.Error())
	}
	if EventEndmarker.String() != "ENDMARKER" || EventIndent.String() != "INDENT" ||
		EventDedent.String() != "DEDENT" || EventNewline.String() != "NEWLINE" {
		t.Fatal("事件名称不正确")
	}
}
