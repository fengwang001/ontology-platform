package regex

import (
	"sync"
	"testing"
)

// bigEngine 构造预算足够大、不会触发超限与封禁的执行器。
func bigEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(1_000_000, 1_000_000_000, 1_000_000_000, 100, 1_000_000_000, 100_000)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustRegister(t *testing.T, e *Engine, id, pat string, memo bool) {
	t.Helper()
	if err := e.Register(id, pat, memo); err != nil {
		t.Fatalf("Register(%q, %q): %v", id, pat, err)
	}
}

func mustMatch(t *testing.T, e *Engine, id, input string, now int64) MatchResult {
	t.Helper()
	r, err := e.Match(id, []byte(input), now)
	if err != nil {
		t.Fatalf("Match(%q, %q, %d): %v", id, input, now, err)
	}
	return r
}

func progOf(t *testing.T, e *Engine, id string) []inst {
	t.Helper()
	p, ok := e.pats[id]
	if !ok {
		t.Fatalf("pattern %q not registered", id)
	}
	return p.prog
}

func checkMatch(t *testing.T, r MatchResult, wantOutcome Outcome, wantStart, wantEnd int, wantSteps int64) {
	t.Helper()
	if r.Outcome != wantOutcome {
		t.Fatalf("outcome = %v, want %v", r.Outcome, wantOutcome)
	}
	if r.Outcome == OutcomeMatch && (r.Start != wantStart || r.End != wantEnd) {
		t.Fatalf("interval = [%d,%d), want [%d,%d)", r.Start, r.End, wantStart, wantEnd)
	}
	if wantSteps >= 0 && r.Steps != wantSteps {
		t.Fatalf("steps = %d, want %d", r.Steps, wantSteps)
	}
}

func TestExampleInstructions(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a*b", false)
	want := []inst{
		{op: opSplit, x: 1, y: 3},
		{op: opChar, b: 'a'},
		{op: opJmp, x: 0},
		{op: opChar, b: 'b'},
		{op: opMatch},
	}
	got := progOf(t, e, "p")
	if len(got) != len(want) {
		t.Fatalf("prog len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inst %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestExampleSteps(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a*b", false)
	checkMatch(t, mustMatch(t, e, "p", "aab", 0), OutcomeMatch, 0, 3, 10)
	checkMatch(t, mustMatch(t, e, "p", "aac", 1), OutcomeNoMatch, 0, 0, 24)
}

func TestExampleStepsMemo(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a*b", true)
	checkMatch(t, mustMatch(t, e, "p", "aab", 0), OutcomeMatch, 0, 3, 10)
	checkMatch(t, mustMatch(t, e, "p", "aac", 1), OutcomeNoMatch, 0, 0, 14)
}

// steps 恰等于 λ 时允许完成；需要 λ+1 步时判超限。
func TestExactLimitBoundary(t *testing.T) {
	e, err := NewEngine(23, 1000, 1000, 100, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	checkMatch(t, mustMatch(t, e, "p", "aac", 0), OutcomeLocalLimit, 0, 0, 23)

	e2, err := NewEngine(24, 1000, 1000, 100, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e2, "p", "a*b", false)
	checkMatch(t, mustMatch(t, e2, "p", "aac", 0), OutcomeNoMatch, 0, 0, 24)
}

// 懒惰与贪婪的优先序：a*? 与 a* 的区间不同。
func TestLazyVsGreedy(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "g", "a*", false)
	mustRegister(t, e, "l", "a*?", false)
	checkMatch(t, mustMatch(t, e, "g", "aa", 0), OutcomeMatch, 0, 2, 9)
	checkMatch(t, mustMatch(t, e, "l", "aa", 1), OutcomeMatch, 0, 0, 2)

	mustRegister(t, e, "gq", "a?b", false)
	mustRegister(t, e, "lq", "a??b", false)
	checkMatch(t, mustMatch(t, e, "gq", "ab", 2), OutcomeMatch, 0, 2, 4)
	// 懒惰 a??b 在 "ab" 上：先试空，b 失配后回溯取 a。
	checkMatch(t, mustMatch(t, e, "lq", "ab", 3), OutcomeMatch, 0, 2, 5)
}

// {m,n} 的指令数为 m×|e|+(n−m)×(|e|+1)，嵌套可选的 Split 跳过时直达整个构造的出口。
func TestCountQuantifierProgram(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a{2,4}", false)
	want := []inst{
		{op: opChar, b: 'a'},      // 0
		{op: opChar, b: 'a'},      // 1
		{op: opSplit, x: 3, y: 6}, // 2：跳过直达出口 6
		{op: opChar, b: 'a'},      // 3
		{op: opSplit, x: 5, y: 6}, // 4：跳过直达出口 6
		{op: opChar, b: 'a'},      // 5
		{op: opMatch},             // 6
	}
	got := progOf(t, e, "p")
	if len(got) != len(want) {
		t.Fatalf("prog len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inst %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// 分组长度 |e|=2：(ab){1,3} → 1×2 + 2×(2+1) = 8 条 + Match。
	mustRegister(t, e, "q", "(ab){1,3}", false)
	if n := len(progOf(t, e, "q")); n != 9 {
		t.Fatalf("(ab){1,3} prog len = %d, want 9", n)
	}
}

func TestCountQuantifierMatch(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a{2,4}", false)
	checkMatch(t, mustMatch(t, e, "p", "aaaaa", 0), OutcomeMatch, 0, 4, -1)
	checkMatch(t, mustMatch(t, e, "p", "aaa", 1), OutcomeMatch, 0, 3, -1)
	checkMatch(t, mustMatch(t, e, "p", "aa", 2), OutcomeMatch, 0, 2, -1)
	checkMatch(t, mustMatch(t, e, "p", "a", 3), OutcomeNoMatch, 0, 0, -1)
	// 懒惰：取最短。
	mustRegister(t, e, "l", "a{2,4}?", false)
	checkMatch(t, mustMatch(t, e, "l", "aaaa", 4), OutcomeMatch, 0, 2, -1)
}

// {m,} 与 {0,1} 边界。
func TestOpenEndedAndBoundaryQuantifier(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a{2,}", false)
	checkMatch(t, mustMatch(t, e, "p", "aaaa", 0), OutcomeMatch, 0, 4, -1)
	checkMatch(t, mustMatch(t, e, "p", "aa", 1), OutcomeMatch, 0, 2, -1)
	checkMatch(t, mustMatch(t, e, "p", "a", 2), OutcomeNoMatch, 0, 0, -1)

	mustRegister(t, e, "z", "a{0,1}", false)
	checkMatch(t, mustMatch(t, e, "z", "a", 3), OutcomeMatch, 0, 1, -1)
	checkMatch(t, mustMatch(t, e, "z", "b", 4), OutcomeMatch, 0, 0, -1)

	mustRegister(t, e, "o", "a{0,}", false)
	checkMatch(t, mustMatch(t, e, "o", "aaa", 5), OutcomeMatch, 0, 3, -1)
}

// checkMatch 的 wantSteps 传 -1 表示不校验步数。

func TestCharClasses(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "range", "[a-c]", false)
	checkMatch(t, mustMatch(t, e, "range", "b", 0), OutcomeMatch, 0, 1, -1)
	checkMatch(t, mustMatch(t, e, "range", "d", 1), OutcomeNoMatch, 0, 0, -1)

	mustRegister(t, e, "neg", "[^a]", false)
	checkMatch(t, mustMatch(t, e, "neg", "\n", 2), OutcomeMatch, 0, 1, -1) // 取反集合含换行
	checkMatch(t, mustMatch(t, e, "neg", "a", 3), OutcomeNoMatch, 0, 0, -1)

	mustRegister(t, e, "set", "[abc]", false)
	checkMatch(t, mustMatch(t, e, "set", "c", 4), OutcomeMatch, 0, 1, -1)

	mustRegister(t, e, "dash", "[-a]", false)
	checkMatch(t, mustMatch(t, e, "dash", "-", 5), OutcomeMatch, 0, 1, -1)

	mustRegister(t, e, "dot", ".", false)
	checkMatch(t, mustMatch(t, e, "dot", "x", 6), OutcomeMatch, 0, 1, -1)
	checkMatch(t, mustMatch(t, e, "dot", "\n", 7), OutcomeNoMatch, 0, 0, -1) // . 不匹配换行

	mustRegister(t, e, "esc", "\\*\\[\\.", false)
	checkMatch(t, mustMatch(t, e, "esc", "*[.", 8), OutcomeMatch, 0, 3, -1)
}

func TestClassSyntaxErrors(t *testing.T) {
	e := bigEngine(t)
	for _, pat := range []string{"[c-a]", "[]", "[^]", "[ab", "["} {
		if err := e.Register("id"+pat, pat, false); err != ErrSyntax {
			t.Fatalf("Register(%q) = %v, want ErrSyntax", pat, err)
		}
	}
}

func TestAssertions(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "empty", "^$", false)
	checkMatch(t, mustMatch(t, e, "empty", "", 0), OutcomeMatch, 0, 0, -1)
	checkMatch(t, mustMatch(t, e, "empty", "a", 1), OutcomeNoMatch, 0, 0, -1)

	mustRegister(t, e, "start", "^a", false)
	checkMatch(t, mustMatch(t, e, "start", "ba", 2), OutcomeNoMatch, 0, 0, -1)
	checkMatch(t, mustMatch(t, e, "start", "ab", 3), OutcomeMatch, 0, 1, -1)

	mustRegister(t, e, "end", "a$", false)
	checkMatch(t, mustMatch(t, e, "end", "ab", 4), OutcomeNoMatch, 0, 0, -1)
	checkMatch(t, mustMatch(t, e, "end", "ba", 5), OutcomeMatch, 1, 2, -1)

	mustRegister(t, e, "dollar", "$", false)
	checkMatch(t, mustMatch(t, e, "dollar", "ab", 6), OutcomeMatch, 2, 2, -1)

	mustRegister(t, e, "caret", "^", false)
	checkMatch(t, mustMatch(t, e, "caret", "ab", 7), OutcomeMatch, 0, 0, -1)
}

// 选择向右嵌套：a|b|c 的指令序列；左侧分支优先。
func TestAlternation(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "alt", "a|b|c", false)
	want := []inst{
		{op: opSplit, x: 1, y: 3},
		{op: opChar, b: 'a'},
		{op: opJmp, x: 7},
		{op: opSplit, x: 4, y: 6},
		{op: opChar, b: 'b'},
		{op: opJmp, x: 7},
		{op: opChar, b: 'c'},
		{op: opMatch},
	}
	got := progOf(t, e, "alt")
	if len(got) != len(want) {
		t.Fatalf("prog len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inst %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	mustRegister(t, e, "prio", "a|ab", false)
	checkMatch(t, mustMatch(t, e, "prio", "ab", 0), OutcomeMatch, 0, 1, -1)
	checkMatch(t, mustMatch(t, e, "alt", "c", 1), OutcomeMatch, 0, 1, -1)
	checkMatch(t, mustMatch(t, e, "alt", "zbc", 2), OutcomeMatch, 1, 2, -1)
}

// 可空重复与语法错误的区分。
func TestNullableRepeat(t *testing.T) {
	e := bigEngine(t)
	nullable := []string{"(a|)*", "()*", "(^)*", "($)?", "(a?)*", "(a{0,2})*", "(|a)?", "((a|b){0,3})+", "(^|a)*"}
	for i, pat := range nullable {
		if err := e.Register(string(rune('a'+i)), pat, false); err != ErrNullableRepeat {
			t.Fatalf("Register(%q) = %v, want ErrNullableRepeat", pat, err)
		}
	}
	// 非可空的合法重复。
	ok := []string{"a*", "(a|b)*", "(ab)+", "a{0,1}", "(a|b{1,2}){2,5}", "a*?", "(a(b|c))*"}
	for i, pat := range ok {
		if err := e.Register(string(rune('k'+i)), pat, false); err != nil {
			t.Fatalf("Register(%q) = %v, want nil", pat, err)
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	e := bigEngine(t)
	bad := []string{
		"a**", "a*?*", "a??+", "a+*", "^*", "$+", "$?", "^{2}", "*a", "+", "?",
		"a{2,1}", "a{0,0}", "a{1,101}", "a{101,}", "a{1", "a{", "a{,2}", "a{2}",
		"(a", "a)", "a\\", "\\", "(a|b", "a{2}*",
	}
	for i, pat := range bad {
		if err := e.Register(string(rune('a'+i)), pat, false); err != ErrSyntax {
			t.Fatalf("Register(%q) = %v, want ErrSyntax", pat, err)
		}
	}
	// 语法错误优先于可空重复：先 nullable 后 syntax 的模式报语法错误。
	if err := e.Register("zz", "(a|)*[", false); err != ErrSyntax {
		t.Fatalf("Register nullable-then-syntax = %v, want ErrSyntax", err)
	}
}

// Register 拒绝次序：参数非法、id 已存在、语法错误、可空重复、程序过大。
func TestRegisterRejectionOrder(t *testing.T) {
	e, err := NewEngine(10, 100, 100, 2, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Register("", "a", false); err != ErrInvalidArgs {
		t.Fatalf("empty id = %v", err)
	}
	longID := string(make([]byte, 65))
	if err := e.Register(longID, "a", false); err != ErrInvalidArgs {
		t.Fatalf("long id = %v", err)
	}
	if err := e.Register("x", "", false); err != ErrInvalidArgs {
		t.Fatalf("empty pattern = %v", err)
	}
	longPat := string(make([]byte, 201))
	if err := e.Register("x", longPat, false); err != ErrInvalidArgs {
		t.Fatalf("long pattern = %v", err)
	}
	mustRegister(t, e, "x", "a", false)
	// id 已存在优先于语法错误与程序过大。
	if err := e.Register("x", "a**", false); err != ErrAlreadyExists {
		t.Fatalf("exists vs syntax = %v", err)
	}
	if err := e.Register("x", "a*b", false); err != ErrAlreadyExists {
		t.Fatalf("exists vs too-large = %v", err)
	}
	// 语法错误优先于可空重复与程序过大。
	if err := e.Register("y", "a**", false); err != ErrSyntax {
		t.Fatalf("syntax = %v", err)
	}
	// 可空重复优先于程序过大（(a|)* 只有 5 条指令也会先报可空重复）。
	if err := e.Register("z", "(a|)*", false); err != ErrNullableRepeat {
		t.Fatalf("nullable = %v", err)
	}
	// P=5：a*b 恰好 5 条可注册，a*bc 6 条过大。
	mustRegister(t, e, "w", "a*b", false)
	if err := e.Register("v", "a*bc", false); err != ErrProgramTooLarge {
		t.Fatalf("too large = %v", err)
	}
}

func TestInvalidConstructorArgs(t *testing.T) {
	cases := [][6]int64{
		{0, 1, 1, 1, 1, 1}, {1_000_001, 1, 1, 1, 1, 1},
		{1, 0, 1, 1, 1, 1}, {1, 1_000_000_001, 1, 1, 1, 1},
		{1, 1, 0, 1, 1, 1}, {1, 1, 1_000_000_001, 1, 1, 1},
		{1, 1, 1, 0, 1, 1}, {1, 1, 1, 101, 1, 1},
		{1, 1, 1, 1, 0, 1}, {1, 1, 1, 1, 1_000_000_001, 1},
		{1, 1, 1, 1, 1, 0}, {1, 1, 1, 1, 1, 100_001},
	}
	for i, c := range cases {
		if _, err := NewEngine(c[0], c[1], c[2], c[3], c[4], c[5]); err != ErrInvalidArgs {
			t.Fatalf("case %d: NewEngine%v = %v, want ErrInvalidArgs", i, c, err)
		}
	}
}

func mustErr(t *testing.T, err error, want error) {
	t.Helper()
	if err != want {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func mustStatus(t *testing.T, e *Engine, id string, now int64) Status {
	t.Helper()
	s, err := e.Status(id, now)
	if err != nil {
		t.Fatalf("Status(%q, %d): %v", id, now, err)
	}
	return s
}

// 题目给定的完整场景：L=10 E=100 G=20 K=2 D=50，模式 a*b 不记忆化。
func TestBudgetWalkthrough(t *testing.T) {
	e, err := NewEngine(10, 100, 20, 2, 50, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)

	// now=0 对 "aac"：rem'=20 >= L，λ=10，本地超限，rem=10，c=1。
	r := mustMatch(t, e, "p", "aac", 0)
	checkMatch(t, r, OutcomeLocalLimit, 0, 0, 10)
	if e.rem != 10 {
		t.Fatalf("rem = %d, want 10", e.rem)
	}
	s := mustStatus(t, e, "p", 0)
	if s.C != 1 || s.B != 0 || s.U != 0 || s.Banned {
		t.Fatalf("status = %+v, want c=1 b=0 u=0 not-banned", s)
	}

	// now=10 同输入：rem'=10 >= L 仍取 λ=10，本地超限，rem=0，c=2 达 K 封禁，
	// b=1，u=10+50=60，c 归 0。
	r = mustMatch(t, e, "p", "aac", 10)
	checkMatch(t, r, OutcomeLocalLimit, 0, 0, 10)
	if e.rem != 0 {
		t.Fatalf("rem = %d, want 0", e.rem)
	}
	s = mustStatus(t, e, "p", 10)
	if s.C != 0 || s.B != 1 || s.U != 60 || !s.Banned {
		t.Fatalf("status = %+v, want c=0 b=1 u=60 banned", s)
	}

	// now=20 被拒为封禁中（先于全局余量判定）。
	_, err = e.Match("p", []byte("aab"), 20)
	mustErr(t, err, ErrBanned)

	// now=60 不再封禁，纪元仍为 0 且 rem'=0，被拒为全局余量为 0。
	_, err = e.Match("p", []byte("aab"), 60)
	mustErr(t, err, ErrGlobalBudgetExhausted)
	if e.maxNow != 10 || e.epoch != 0 || e.rem != 0 {
		t.Fatalf("rejected call mutated state: maxNow=%d epoch=%d rem=%d", e.maxNow, e.epoch, e.rem)
	}

	// now=100 进入纪元 1，rem 重置为 20，对 "aab" 匹配耗 10 步，rem=10，c 归 0。
	r = mustMatch(t, e, "p", "aab", 100)
	checkMatch(t, r, OutcomeMatch, 0, 3, 10)
	if e.epoch != 1 || e.rem != 10 {
		t.Fatalf("epoch = %d rem = %d, want 1/10", e.epoch, e.rem)
	}
	s = mustStatus(t, e, "p", 100)
	if s.C != 0 || s.B != 1 || s.U != 60 || s.Banned {
		t.Fatalf("status = %+v, want c=0 b=1 u=60 not-banned", s)
	}
}

// rem' 恰等于 L 时超限归类为本地超限；rem' < L 时为全局超限。
func TestLimitClassification(t *testing.T) {
	e, err := NewEngine(10, 1000, 15, 100, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	// rem'=15 >= L=10 → λ=10，本地超限；rem 剩 5。
	checkMatch(t, mustMatch(t, e, "p", "aac", 0), OutcomeLocalLimit, 0, 0, 10)
	if e.rem != 5 {
		t.Fatalf("rem = %d, want 5", e.rem)
	}
	// rem'=5 < L → λ=5，全局超限；rem=0。
	checkMatch(t, mustMatch(t, e, "p", "aac", 1), OutcomeGlobalLimit, 0, 0, 5)
	if e.rem != 0 {
		t.Fatalf("rem = %d, want 0", e.rem)
	}
}

// 全局超限不改 c 也不清零；匹配与无匹配使 c 归 0。
func TestGlobalLimitKeepsC(t *testing.T) {
	e, err := NewEngine(10, 1000, 25, 100, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	// 本地超限 → c=1，rem=15。
	checkMatch(t, mustMatch(t, e, "p", "aac", 0), OutcomeLocalLimit, 0, 0, 10)
	// 本地超限 → c=2，rem=5。
	checkMatch(t, mustMatch(t, e, "p", "aac", 1), OutcomeLocalLimit, 0, 0, 10)
	// rem'=5 < L → 全局超限，c 保持 2。
	checkMatch(t, mustMatch(t, e, "p", "aac", 2), OutcomeGlobalLimit, 0, 0, 5)
	if s := mustStatus(t, e, "p", 2); s.C != 2 {
		t.Fatalf("c = %d, want 2 (global limit must not change c)", s.C)
	}
	// 新纪元匹配成功 → c 归 0。
	checkMatch(t, mustMatch(t, e, "p", "aab", 1000), OutcomeMatch, 0, 3, 10)
	if s := mustStatus(t, e, "p", 1000); s.C != 0 {
		t.Fatalf("c = %d, want 0 after match", s.C)
	}
	// 无匹配也使 c 归 0：先造一次本地超限。
	checkMatch(t, mustMatch(t, e, "p", "aac", 1001), OutcomeLocalLimit, 0, 0, 10)
	if s := mustStatus(t, e, "p", 1001); s.C != 1 {
		t.Fatalf("c = %d, want 1", s.C)
	}
	// 空输入上无匹配只需 3 步，rem'=5 足够。
	checkMatch(t, mustMatch(t, e, "p", "", 1002), OutcomeNoMatch, 0, 0, 3)
	if s := mustStatus(t, e, "p", 1002); s.C != 0 {
		t.Fatalf("c = %d, want 0 after no-match", s.C)
	}
}

// c 恰达 K 封禁；b=1/2/3/4 时时长 D/2D/4D/8D，之后封顶 8D；now 恰等于 u 不再封禁。
func TestBanEscalation(t *testing.T) {
	e, err := NewEngine(10, 1_000_000_000, 1_000_000_000, 1, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	now := int64(0)
	wantDur := []int64{100, 200, 400, 800, 800}
	for i, dur := range wantDur {
		checkMatch(t, mustMatch(t, e, "p", "aac", now), OutcomeLocalLimit, 0, 0, 10)
		s := mustStatus(t, e, "p", now)
		wantB := int64(i + 1)
		if s.B != wantB || s.U != now+dur || s.C != 0 {
			t.Fatalf("round %d: status = %+v, want b=%d u=%d c=0", i, s, wantB, now+dur)
		}
		// 封禁中：now+1 < u。
		if !mustStatus(t, e, "p", now+1).Banned {
			t.Fatalf("round %d: should be banned at %d", i, now+1)
		}
		_, err := e.Match("p", []byte("aab"), now+1)
		mustErr(t, err, ErrBanned)
		// now 恰等于 u 不再封禁。
		now += dur
		if mustStatus(t, e, "p", now).Banned {
			t.Fatalf("round %d: should not be banned at u=%d", i, now)
		}
	}
}

// 纪元边界：now 恰为 E 的倍数时进入新纪元并重置 rem。
func TestEpochBoundary(t *testing.T) {
	e, err := NewEngine(10, 100, 15, 100, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	// now=99 仍在纪元 0，本地超限后 rem=5。
	checkMatch(t, mustMatch(t, e, "p", "aac", 99), OutcomeLocalLimit, 0, 0, 10)
	if e.epoch != 0 || e.rem != 5 {
		t.Fatalf("epoch=%d rem=%d, want 0/5", e.epoch, e.rem)
	}
	// now=100 恰为 E 的倍数 → 纪元 1，rem 重置为 15，λ=10 本地超限。
	checkMatch(t, mustMatch(t, e, "p", "aac", 100), OutcomeLocalLimit, 0, 0, 10)
	if e.epoch != 1 || e.rem != 5 {
		t.Fatalf("epoch=%d rem=%d, want 1/5", e.epoch, e.rem)
	}
	// now=199 仍是纪元 1，rem'=5 < L → 全局超限。
	checkMatch(t, mustMatch(t, e, "p", "aac", 199), OutcomeGlobalLimit, 0, 0, 5)
	// now=200 进入纪元 2。
	checkMatch(t, mustMatch(t, e, "p", "aab", 200), OutcomeMatch, 0, 3, 10)
	if e.epoch != 2 || e.rem != 5 {
		t.Fatalf("epoch=%d rem=%d, want 2/5", e.epoch, e.rem)
	}
}

// 拒绝次序：参数非法、未注册、时钟回退、封禁中、全局余量为 0。
func TestMatchRejectionOrder(t *testing.T) {
	e, err := NewEngine(10, 100, 20, 1, 30, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	// 参数非法优先于未注册。
	if _, err := e.Match("", []byte("a"), 0); err != ErrInvalidArgs {
		t.Fatalf("empty id = %v", err)
	}
	if _, err := e.Match("nope", make([]byte, 65537), 0); err != ErrInvalidArgs {
		t.Fatalf("long input = %v", err)
	}
	if _, err := e.Match("nope", []byte("a"), MaxNow+1); err != ErrInvalidArgs {
		t.Fatalf("now overflow = %v", err)
	}
	if _, err := e.Match("nope", []byte("a"), -1); err != ErrInvalidArgs {
		t.Fatalf("negative now = %v", err)
	}
	// 未注册优先于时钟回退：先推进 maxNow。
	checkMatch(t, mustMatch(t, e, "p", "aab", 50), OutcomeMatch, 0, 3, 10)
	if _, err := e.Match("nope", []byte("a"), 10); err != ErrNotRegistered {
		t.Fatalf("unregistered vs regression = %v", err)
	}
	// 时钟回退优先于封禁：先让模式封禁（K=1，本地超限即封，D=30 → u=90）。
	checkMatch(t, mustMatch(t, e, "p", "aac", 60), OutcomeLocalLimit, 0, 0, 10)
	if s := mustStatus(t, e, "p", 60); !s.Banned || s.U != 90 {
		t.Fatalf("status = %+v, want banned u=90", s)
	}
	if _, err := e.Match("p", []byte("a"), 55); err != ErrClockRegression {
		t.Fatalf("regression vs banned = %v", err)
	}
	// 封禁优先于全局余量：rem 已为 0（20-10-10），仍报封禁。
	if _, err := e.Match("p", []byte("a"), 70); err != ErrBanned {
		t.Fatalf("banned vs global = %v", err)
	}
	// 封禁结束（now=90）后报全局余量为 0。
	if _, err := e.Match("p", []byte("a"), 90); err != ErrGlobalBudgetExhausted {
		t.Fatalf("global = %v", err)
	}
}

// 被拒绝的操作不推进最大 now、纪元与余量。
func TestRejectedCallsDoNotAdvance(t *testing.T) {
	e, err := NewEngine(10, 100, 20, 100, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, e, "p", "a*b", false)
	checkMatch(t, mustMatch(t, e, "p", "aab", 10), OutcomeMatch, 0, 3, 10)
	// 一系列拒绝：未注册、时钟回退、参数非法、（未来的）大 now 上的未注册。
	if _, err := e.Match("nope", []byte("a"), 10); err != ErrNotRegistered {
		t.Fatal(err)
	}
	if _, err := e.Match("p", []byte("a"), 5); err != ErrClockRegression {
		t.Fatal(err)
	}
	if _, err := e.Match("p", []byte("a"), MaxNow+1); err != ErrInvalidArgs {
		t.Fatal(err)
	}
	if _, err := e.Match("nope", []byte("a"), 900); err != ErrNotRegistered {
		t.Fatal(err)
	}
	if e.maxNow != 10 || e.epoch != 0 || e.rem != 10 {
		t.Fatalf("state mutated: maxNow=%d epoch=%d rem=%d", e.maxNow, e.epoch, e.rem)
	}
	// now=11 仍被接受（maxNow 未被拒绝调用推进）。
	checkMatch(t, mustMatch(t, e, "p", "aab", 11), OutcomeMatch, 0, 3, 10)
}

func TestStatusQuery(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "a", false)
	if _, err := e.Status("", 0); err != ErrInvalidArgs {
		t.Fatalf("empty id = %v", err)
	}
	if _, err := e.Status("p", -1); err != ErrInvalidArgs {
		t.Fatalf("negative now = %v", err)
	}
	if _, err := e.Status("nope", 0); err != ErrNotRegistered {
		t.Fatalf("unregistered = %v", err)
	}
	s := mustStatus(t, e, "p", 0)
	if s.C != 0 || s.B != 0 || s.U != 0 || s.Banned {
		t.Fatalf("status = %+v, want zero", s)
	}
}

// (a+)+b 一类灾难性模式：记忆化关闭时步数随输入增长，开启后受 程序指令数×(n+1) 限制。
func TestCatastrophicMemoBound(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "off", "(a+)+b", false)
	mustRegister(t, e, "on", "(a+)+b", true)
	progLen := int64(len(progOf(t, e, "off")))
	prevOff := int64(0)
	for n := 4; n <= 16; n += 4 {
		input := ""
		for i := 0; i < n; i++ {
			input += "a"
		}
		ro := mustMatch(t, e, "off", input, int64(n))
		rm := mustMatch(t, e, "on", input, int64(n))
		if ro.Outcome != OutcomeNoMatch || rm.Outcome != OutcomeNoMatch {
			t.Fatalf("n=%d: outcomes %v/%v, want NoMatch/NoMatch", n, ro.Outcome, rm.Outcome)
		}
		if bound := progLen * int64(n+1); rm.Steps > bound {
			t.Fatalf("n=%d: memo steps %d > bound %d", n, rm.Steps, bound)
		}
		if n > 4 && ro.Steps <= prevOff {
			t.Fatalf("n=%d: non-memo steps %d not growing (prev %d)", n, ro.Steps, prevOff)
		}
		prevOff = ro.Steps
		if n == 16 && ro.Steps <= rm.Steps {
			t.Fatalf("n=%d: non-memo steps %d should exceed memo steps %d", n, ro.Steps, rm.Steps)
		}
		t.Logf("n=%d non-memo=%d memo=%d bound=%d", n, ro.Steps, rm.Steps, progLen*int64(n+1))
	}
}

// 无超限情景下记忆化开关的匹配结果与区间必须一致。
func TestMemoConsistency(t *testing.T) {
	e := bigEngine(t)
	patterns := []string{
		"a*b", "(a+)+b", "a|ab", "(ab|a)*b", "a{2,5}b", "[a-c]+x", "^a+b$",
		"(a|b){1,3}c", "a+?b", "x(a|aa)+z",
	}
	inputs := []string{"", "a", "aa", "aab", "ab", "b", "aaab", "ax", "aax", "xaaaz", "abc", "ac", "ba"}
	for i, pat := range patterns {
		idOff := string(rune('A' + i))
		idOn := string(rune('a' + i))
		mustRegister(t, e, idOff, pat, false)
		mustRegister(t, e, idOn, pat, true)
		for j, in := range inputs {
			now := int64(i*100 + j)
			ro := mustMatch(t, e, idOff, in, now)
			rm := mustMatch(t, e, idOn, in, now)
			if ro.Outcome != rm.Outcome || ro.Start != rm.Start || ro.End != rm.End {
				t.Fatalf("pattern %q input %q: off=%+v on=%+v", pat, in, ro, rm)
			}
			if ro.Steps < rm.Steps {
				t.Fatalf("pattern %q input %q: memo steps %d > non-memo %d", pat, in, rm.Steps, ro.Steps)
			}
		}
	}
}

// 非导出计数器：实际调度总数与返回的 steps 之和相等。
func TestDispatchedCounter(t *testing.T) {
	e := bigEngine(t)
	mustRegister(t, e, "p", "(a+)+b", true)
	var total int64
	for i := 0; i < 20; i++ {
		input := ""
		for j := 0; j < i; j++ {
			input += "a"
		}
		r := mustMatch(t, e, "p", input, int64(i))
		total += r.Steps
	}
	if e.dispatched != total {
		t.Fatalf("dispatched = %d, want %d", e.dispatched, total)
	}
}

// 相同的操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() []MatchResult {
		e, err := NewEngine(12, 50, 30, 2, 40, 100)
		if err != nil {
			t.Fatal(err)
		}
		mustRegister(t, e, "p", "a*b", false)
		mustRegister(t, e, "q", "(a|b)+c", true)
		var out []MatchResult
		ops := []struct {
			id    string
			input string
			now   int64
		}{
			{"p", "aac", 0}, {"p", "aab", 3}, {"q", "abac", 7}, {"p", "b", 12},
			{"q", "zzz", 20}, {"p", "aac", 33}, {"q", "abc", 49}, {"p", "aab", 50},
			{"q", "c", 51}, {"p", "aaab", 100},
		}
		for _, op := range ops {
			r, err := e.Match(op.id, []byte(op.input), op.now)
			if err != nil {
				out = append(out, MatchResult{Outcome: Outcome(-1), Steps: -1})
				continue
			}
			out = append(out, r)
		}
		return out
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(first) {
			t.Fatalf("replay %d: len %d != %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("replay %d op %d: %+v != %+v", i, j, got[j], first[j])
			}
		}
	}
}

// 并发调用：结果等价于某个串行顺序，且不变量始终成立。
func TestConcurrency(t *testing.T) {
	e, err := NewEngine(20, 10, 50, 3, 5, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p", "q", "r"} {
		mustRegister(t, e, id, "a*b", false)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ids := []string{"p", "q", "r"}
			for i := 0; i < 500; i++ {
				id := ids[(w+i)%3]
				now := int64(i / 4)
				r, err := e.Match(id, []byte("aac"), now)
				if err == nil {
					if r.Steps > 20 {
						t.Errorf("steps %d exceed lambda <= L=20", r.Steps)
					}
				}
				s, serr := e.Status(id, now)
				if serr == nil {
					if s.C < 0 || s.C >= 3 {
						t.Errorf("c = %d out of [0,K)", s.C)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rem < 0 || e.rem > e.g {
		t.Fatalf("rem = %d out of [0,%d]", e.rem, e.g)
	}
	for id, p := range e.pats {
		if p.c < 0 || p.c >= e.k {
			t.Fatalf("pattern %s c = %d out of [0,%d)", id, p.c, e.k)
		}
	}
}
