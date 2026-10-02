package layout

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// 随机测试的语句片段池。
var randStmts = []string{
	"x = 1", "y", "z = 2", "pass", "return", "x = 1 + 2", "x: y",
	"foo(1,", "2)", "bar(1, 2)", "x = (", ")", "(", "[", "]", "{", "}",
	"d = ':'", `e = ":"`, "f = '#'", "g = '('", "h = ')'", "m = '\\\\'",
	"s = 'abc'", `s = "a\"b"`, "s = 'abc", "s = 'ab\\", "s = 'a\\'",
	"x = 1 # c", "x = 1 # ([{", "x = 1 # '",
	`x = 1 + \`, `\`, "y = \\ ", "w = \\ x",
	"x = (]", "y = )", "z = (}", "([)]",
	":", "::",
}

var randHeaders = []string{
	"if x:", "if y:", "try:", "for i in r:", "while q:",
	"if x: # c", "if x: \\", "elsewhere:", "else_:", "elif2:",
}

var randBranches = []string{
	"else:", "elif z:", "except:", "finally:", "elsewhere:",
}

var randBlanks = []string{
	"", "   ", "\t", "\t\t", "# c", "  # c", "\t# x", " \t ",
}

var randPrefixes = []string{
	"", " ", "  ", "   ", "    ", "        ", "\t", "\t\t",
	" \t", "\t ", "   \t", "\t   ", "  \t  ",
}

func pick(rng *rand.Rand, xs []string) string {
	return xs[rng.Intn(len(xs))]
}

// seqGen 按两种模式生成行序列：结构化（维护虚拟缩进层级）与
// 片段杂烩。
type seqGen struct {
	rng   *rand.Rand
	level int
	tabs  bool
}

func (g *seqGen) indentAt(level int) string {
	if g.tabs {
		return strings.Repeat("\t", level)
	}
	return strings.Repeat("  ", level)
}

func (g *seqGen) structured() string {
	r := g.rng.Intn(100)
	switch {
	case r < 28:
		return g.indentAt(g.level) + pick(g.rng, randStmts[:18])
	case r < 46:
		lvl := g.level
		if g.level < 6 {
			g.level++
		}
		return g.indentAt(lvl) + pick(g.rng, randHeaders)
	case r < 58:
		return g.indentAt(g.level) + pick(g.rng, randBranches)
	case r < 70:
		if g.level > 0 {
			g.level--
		}
		return g.indentAt(g.level) + pick(g.rng, randStmts[:18])
	case r < 78:
		return pick(g.rng, randBlanks)
	case r < 90:
		return g.indentAt(g.level) + pick(g.rng, randStmts)
	default:
		// 风格混杂，可能触发制表符不一致。
		return pick(g.rng, randPrefixes) + pick(g.rng, randStmts[:18])
	}
}

func (g *seqGen) soup() string {
	if g.rng.Intn(100) < 3 {
		n := MaxLineBytes - 1 + g.rng.Intn(3) // 恰好、超一、超二
		return strings.Repeat("x", n)
	}
	return pick(g.rng, randPrefixes) + pick(g.rng, randStmts)
}

func reasonOf(err *Error) Reason {
	if err == nil {
		return ""
	}
	return err.Reason
}

// checkInvariants 验证规格要求的不变量。
func checkInvariants(t *testing.T, l *Layout, indents, dedents int) {
	t.Helper()
	for i := 1; i < len(l.stack); i++ {
		if l.stack[i].c <= l.stack[i-1].c || l.stack[i].a <= l.stack[i-1].a {
			t.Fatalf("缩进栈不严格递增: %v", l.stack)
		}
	}
	if len(l.stack)-1 > l.dm {
		t.Fatalf("缩进栈深 %d 超过 Dm=%d", len(l.stack)-1, l.dm)
	}
	if len(l.brackets) > l.bm {
		t.Fatalf("括号栈深 %d 超过 Bm=%d", len(l.brackets), l.bm)
	}
	if dedents > indents {
		t.Fatalf("累计 DEDENT=%d 超过累计 INDENT=%d", dedents, indents)
	}
}

// TestRandomDifferential 用 2000 组随机行序列对照增量实现与朴素
// 模拟（每次 Feed 都从当前逻辑行的第一个物理行起整体重扫），并
// 验证重放一致性与不变量。日志打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	seed := int64(20261003)
	if s := os.Getenv("LAYOUT_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rng := rand.New(rand.NewSource(seed))
	for seq := 0; seq < 2000; seq++ {
		runRandomSeq(t, rng, seq)
	}
}

func runRandomSeq(t *testing.T, rng *rand.Rand, seq int) {
	dm := 1 + rng.Intn(4)
	if rng.Intn(5) == 0 {
		dm = 1 + rng.Intn(100)
	}
	bm := 1 + rng.Intn(5)
	if rng.Intn(5) == 0 {
		bm = 1 + rng.Intn(200)
	}
	l1, err := New(dm, bm)
	if err != nil {
		t.Fatalf("seq %d: New 被拒: %v", seq, err)
	}
	l2, err := New(dm, bm)
	if err != nil {
		t.Fatalf("seq %d: New 被拒: %v", seq, err)
	}
	nv := newNaive(dm, bm)
	gen := &seqGen{rng: rng, tabs: rng.Intn(2) == 0}
	structured := rng.Intn(2) == 0

	t.Logf("seq=%d dm=%d bm=%d mode=%s tabs=%v",
		seq, dm, bm, map[bool]string{true: "structured", false: "soup"}[structured], gen.tabs)

	nLines := 1 + rng.Intn(30)
	indents, dedents := 0, 0
	wantScanned := 0
	count := func(ev []Event) {
		for _, e := range ev {
			switch e {
			case EventIndent:
				indents++
			case EventDedent:
				dedents++
			}
		}
	}
	step := func(line string) {
		// 计数器期望：延续行或非空白行会被扫描；被拒绝的行不计。
		scanned := l1.incomplete || !isBlankOrComment(line)
		ev1, err1 := l1.Feed(line)
		ev2, err2 := l2.Feed(line)
		evn, errn := nv.Feed(line)
		c, a := indentWidth(line)
		t.Logf("  feed %-28q c=%d a=%d -> ev=%v err=%v | stack=%v brackets=%q incomp=%v expect=%v",
			line, c, a, ev1, reasonOf(err1), l1.stack, l1.brackets, l1.incomplete, l1.colonExpected)
		if !eventsEqual(ev1, evn) || reasonOf(err1) != reasonOf(errn) {
			t.Fatalf("seq %d: Feed(%q) 与朴素模拟不一致: 增量 ev=%v err=%v, 朴素 ev=%v err=%v",
				seq, line, ev1, reasonOf(err1), evn, reasonOf(errn))
		}
		if !eventsEqual(ev1, ev2) || reasonOf(err1) != reasonOf(err2) {
			t.Fatalf("seq %d: Feed(%q) 重放不一致: %v/%v vs %v/%v",
				seq, line, ev1, reasonOf(err1), ev2, reasonOf(err2))
		}
		if !sameState(snap(l1), nv.snapshot()) {
			t.Fatalf("seq %d: Feed(%q) 后状态不一致:\n增量: %+v\n朴素: %+v",
				seq, line, snap(l1), nv.snapshot())
		}
		if err1 == nil {
			count(ev1)
			if scanned {
				wantScanned += len(line)
			}
		}
		checkInvariants(t, l1, indents, dedents)
	}
	for i := 0; i < nLines; i++ {
		if structured {
			step(gen.structured())
		} else {
			step(gen.soup())
		}
	}
	// 大多数序列以 Close 结束，随后再验证已关闭拒绝。
	if rng.Intn(5) > 0 {
		ev1, err1 := l1.Close()
		ev2, err2 := l2.Close()
		evn, errn := nv.Close()
		t.Logf("  close -> ev=%v err=%v", ev1, reasonOf(err1))
		if !eventsEqual(ev1, evn) || reasonOf(err1) != reasonOf(errn) {
			t.Fatalf("seq %d: Close 与朴素模拟不一致: 增量 %v/%v, 朴素 %v/%v",
				seq, ev1, reasonOf(err1), evn, reasonOf(errn))
		}
		if !eventsEqual(ev1, ev2) || reasonOf(err1) != reasonOf(err2) {
			t.Fatalf("seq %d: Close 重放不一致", seq)
		}
		if err1 == nil {
			count(ev1)
			if indents != dedents {
				t.Fatalf("seq %d: Close 后 INDENT=%d 与 DEDENT=%d 不相等", seq, indents, dedents)
			}
			if !sameState(snap(l1), nv.snapshot()) {
				t.Fatalf("seq %d: Close 后状态不一致", seq)
			}
			// 已关闭后 Feed 与 Close 都拒绝。
			step("x = 1")
			if _, err := l1.Close(); err == nil || err.Reason != ReasonClosed {
				t.Fatalf("seq %d: 重复 Close 应拒绝为已关闭, 得到 %v", seq, err)
			}
		}
	}
	if l1.scannedBytes != wantScanned {
		t.Fatalf("seq %d: scannedBytes = %d, 期望 %d", seq, l1.scannedBytes, wantScanned)
	}
	if !sameState(snap(l1), snap(l2)) {
		t.Fatalf("seq %d: 重放最终状态不一致", seq)
	}
}
