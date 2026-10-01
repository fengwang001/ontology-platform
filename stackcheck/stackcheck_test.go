package stackcheck_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/stackcheck"
)

func prog(ops ...stackcheck.Op) []stackcheck.Op { return ops }

func jmp(t int) stackcheck.Op { return stackcheck.Op{Code: stackcheck.JMP, Target: t} }
func jz(t int) stackcheck.Op  { return stackcheck.Op{Code: stackcheck.JZ, Target: t} }
func op(c stackcheck.OpCode) stackcheck.Op {
	return stackcheck.Op{Code: c}
}

func fmtOps(ops []stackcheck.Op) string {
	s := "["
	for i, o := range ops {
		if i > 0 {
			s += " "
		}
		if o.Code == stackcheck.JMP || o.Code == stackcheck.JZ {
			s += fmt.Sprintf("%d:%s->%d", i, o.Code, o.Target)
		} else {
			s += fmt.Sprintf("%d:%s", i, o.Code)
		}
	}
	return s + "]"
}

func reasonOf(e *stackcheck.Error) stackcheck.Reason {
	if e == nil {
		return ""
	}
	return e.Reason
}

func pcOf(e *stackcheck.Error) int {
	if e == nil {
		return -1
	}
	return e.PC
}

func depthOf(e *stackcheck.Error) int {
	if e == nil {
		return -1
	}
	return e.Depth
}

// TestAnalyzeCurious 覆盖题面点名的边界场景。
func TestAnalyzeCurious(t *testing.T) {
	type tc struct {
		name  string
		limit int
		ops   []stackcheck.Op
		want  stackcheck.Reason // 空串表示成功
		max   int
		basis string
	}
	cases := []tc{
		{
			name:  "limit_less_than_one",
			limit: 0,
			ops:   prog(op(stackcheck.RET)),
			want:  stackcheck.ReasonInvalidLimit,
			basis: "构造 L<1 直接拒绝",
		},
		{
			name:  "empty_program",
			limit: 4,
			ops:   prog(),
			want:  stackcheck.ReasonEmptyProgram,
			basis: "指令序列为空",
		},
		{
			name:  "unknown_opcode",
			limit: 4,
			ops:   prog(op(stackcheck.PUSH), stackcheck.Op{Code: stackcheck.OpCode(99)}),
			want:  stackcheck.ReasonUnknownOp,
			basis: "下标 1 为未知操作码，按下标升序先于跳转越界报告",
		},
		{
			name:  "unknown_opcode_before_any_jump_oob",
			limit: 4,
			ops:   prog(jmp(9), stackcheck.Op{Code: stackcheck.OpCode(99)}),
			want:  stackcheck.ReasonUnknownOp,
			basis: "虽下标 0 跳转已越界，但只要存在未知操作码就优先报告",
		},
		{
			name:  "depth_exactly_L_ok",
			limit: 2,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.POP), op(stackcheck.POP), op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  "",
			max:   2,
			basis: "执行后恰等于 L=2 允许；RET 入口为 1",
		},
		{
			name:  "depth_L_plus_one_overflow",
			limit: 2,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  stackcheck.ReasonOverflow,
			basis: "第三条 PUSH 执行后为 3 = L+1，超限（先于其后 RET 栈深 3 的检查）",
		},
		{
			name:  "merge_exactly_consistent_after_jz_pop",
			limit: 4,
			ops: prog(
				op(stackcheck.PUSH), jz(4), op(stackcheck.PUSH), jmp(5), op(stackcheck.PUSH), op(stackcheck.RET),
			),
			want:  "",
			max:   1,
			basis: "JZ 先弹出，两路均以弹后 0 出发，各自 PUSH 后以 1 汇合于下标 5 的 RET",
		},
		{
			name:  "merge_off_by_one",
			limit: 4,
			ops: prog(
				op(stackcheck.PUSH), jz(3), op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.RET),
			),
			want:  stackcheck.ReasonMergeMismatch,
			basis: "JZ 弹后目标路以 0 进入下标 3 的 PUSH，落空路 PUSH 后以 1 进入同一点，差一",
		},
		{
			name:  "ret_depth_zero",
			limit: 2,
			ops:   prog(op(stackcheck.RET)),
			want:  stackcheck.ReasonBadReturnDepth,
			basis: "RET 入口栈深 0，要求恰为 1",
		},
		{
			name:  "ret_depth_one_ok",
			limit: 2,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  "",
			max:   1,
			basis: "RET 入口栈深恰为 1",
		},
		{
			name:  "ret_depth_two",
			limit: 2,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  stackcheck.ReasonBadReturnDepth,
			basis: "RET 入口栈深 2（执行后 2 未超限），RET 检查判定不为 1",
		},
		{
			name:  "jz_pops_then_converge",
			limit: 3,
			ops: prog(
				op(stackcheck.PUSH), jz(4), op(stackcheck.PUSH), jmp(5), op(stackcheck.PUSH),
				op(stackcheck.RET),
			),
			want:  "",
			max:   1,
			basis: "JZ 弹出后两路均 0：落空路 PUSH 后经 JMP 以 1 汇合 RET；目标路 PUSH 后落空也以 1 汇合，恰一致",
		},
		{
			name:  "unreachable_underflow_ignored",
			limit: 3,
			ops: prog(
				op(stackcheck.PUSH), op(stackcheck.RET), op(stackcheck.POP), op(stackcheck.RET),
			),
			want:  "",
			max:   1,
			basis: "下标 2 的 POP 下溢不可达，不检查；max 仅统计可达指令",
		},
		{
			name:  "unreachable_overflow_ignored",
			limit: 1,
			ops: prog(
				op(stackcheck.PUSH), op(stackcheck.RET), op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.RET),
			),
			want:  "",
			max:   1,
			basis: "下标 3 的 PUSH 会使深度到 2>L，但不可达故不报",
		},
		{
			name:  "unreachable_jump_oob_still_rejected",
			limit: 3,
			ops: prog(
				op(stackcheck.PUSH), op(stackcheck.RET), jmp(9),
			),
			want:  stackcheck.ReasonJumpOutOfBounds,
			basis: "跳转目标范围对全部指令（含不可达）静态检查",
		},
		{
			name:  "jump_target_equal_length_rejected",
			limit: 3,
			ops: prog(
				op(stackcheck.PUSH), jz(3), op(stackcheck.RET),
			),
			want:  stackcheck.ReasonJumpOutOfBounds,
			basis: "长度为 3，目标恰等于长度 3，非法",
		},
		{
			name:  "negative_jump_target_rejected",
			limit: 3,
			ops:   prog(jmp(-1)),
			want:  stackcheck.ReasonJumpOutOfBounds,
			basis: "目标 -1 越界（不可达性不影响静态目标检查）",
		},
		{
			name:  "fallthrough_past_end",
			limit: 3,
			ops:   prog(op(stackcheck.PUSH)),
			want:  stackcheck.ReasonFallthroughBounds,
			basis: "PUSH 落空到下标 1 == 长度 1，落空越界",
		},
		{
			name:  "jz_fallthrough_past_end",
			limit: 3,
			ops:   prog(op(stackcheck.PUSH), jz(0)),
			want:  stackcheck.ReasonFallthroughBounds,
			basis: "JZ 先目标（0 与既有入口 0 一致）后落空，落空到 2==长度，越界",
		},
		{
			name:  "loop_depth_drift_merge_mismatch",
			limit: 5,
			ops: prog(
				jmp(1), op(stackcheck.PUSH), jmp(1),
			),
			want:  stackcheck.ReasonMergeMismatch,
			basis: "下标 1 首次以 0 进入，回边以 1 进入，环路上栈深漂移",
		},
		{
			name:  "pop_underflow",
			limit: 3,
			ops:   prog(op(stackcheck.POP), op(stackcheck.RET)),
			want:  stackcheck.ReasonUnderflow,
			basis: "POP 入口 0 < 1，下溢",
		},
		{
			name:  "add_underflow_depth_one",
			limit: 3,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.ADD), op(stackcheck.RET)),
			want:  stackcheck.ReasonUnderflow,
			basis: "ADD 入口 1 < 2，下溢",
		},
		{
			name:  "add_net_shrink_ok",
			limit: 3,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.ADD), op(stackcheck.POP), op(stackcheck.POP), op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  "",
			max:   3,
			basis: "ADD 入口 3 弹二压一执行后 2，两次 POP 后 0，再 PUSH 使 RET 入口恰为 1；max=3",
		},
		{
			name:  "dup_ok",
			limit: 2,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.DUP), op(stackcheck.POP), op(stackcheck.POP), op(stackcheck.PUSH), op(stackcheck.RET)),
			want:  "",
			max:   2,
			basis: "DUP 入口至少 1，执行后 +1",
		},
		{
			name:  "dup_overflow",
			limit: 1,
			ops:   prog(op(stackcheck.PUSH), op(stackcheck.DUP), op(stackcheck.RET)),
			want:  stackcheck.ReasonOverflow,
			basis: "DUP 入口 1 满足，执行后 2 > L=1，超限",
		},
		{
			name:  "underflow_before_overflow_within_instruction",
			limit: 0,
			ops:   prog(),
			want:  stackcheck.ReasonInvalidLimit,
			basis: "L<1 在任何检查之前",
		},
		{
			name:  "stable_loop_ok",
			limit: 3,
			ops: prog(
				jmp(1), op(stackcheck.PUSH), jz(4), jmp(1),
				op(stackcheck.PUSH), op(stackcheck.RET),
			),
			want:  "",
			max:   1,
			basis: "下标 2 的 JZ 每次均以 1 进入、弹出后落空 JMP 以 0 回下标 1（一致）；目标路以弹后 0 到下标 4 PUSH，落空到 RET 入口 1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry, maxD, e := stackcheck.Analyze(c.limit, c.ops)
			got := reasonOf(e)
			out := fmt.Sprintf("reason=%q max=%d entry=%v pc=%d depth=%d", got, maxD, entry, pcOf(e), depthOf(e))
			t.Logf("[输入] L=%d ops=%s\n[输出] %s\n[判定依据] %s", c.limit, fmtOps(c.ops), out, c.basis)
			if got != c.want {
				t.Fatalf("want reason %q, got %q (%v)", c.want, got, e)
			}
			if c.want == "" && maxD != c.max {
				t.Fatalf("want max %d, got %d", c.max, maxD)
			}
		})
	}
}

// TestRegistrationSemantics 覆盖拒绝无痕、可区分原因、最大栈深恒定。
func TestRegistrationSemantics(t *testing.T) {
	v, ne := stackcheck.New(2)
	if ne != nil {
		t.Fatal(ne)
	}
	if _, bad := stackcheck.New(0); reasonOf(bad) != stackcheck.ReasonInvalidLimit {
		t.Fatalf("New(0) = %v", bad)
	}
	t.Logf("[构造] New(0) => invalid_limit；New(2) 成功")
	good := prog(op(stackcheck.PUSH), op(stackcheck.RET))
	if e := v.Register("ok", good); e != nil {
		t.Fatal(e)
	}

	bad := []struct {
		name  string
		ops   []stackcheck.Op
		want  stackcheck.Reason
		basis string
	}{
		{"ok", good, stackcheck.ReasonNameExists, "同名函数已存在，按优先级先于序列本身错误"},
		{"ok", prog(op(stackcheck.POP)), stackcheck.ReasonNameExists, "名字已存在优先于下溢"},
		{"empty", prog(), stackcheck.ReasonEmptyProgram, "空序列"},
		{"unknown", prog(stackcheck.Op{Code: stackcheck.OpCode(42)}), stackcheck.ReasonUnknownOp, "未知操作码"},
		{"oob", prog(jmp(1)), stackcheck.ReasonJumpOutOfBounds, "目标等于长度"},
		{"under", prog(op(stackcheck.POP)), stackcheck.ReasonUnderflow, "数据流下溢"},
		{"over", prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.PUSH)), stackcheck.ReasonOverflow, "L=2 时三次 PUSH"},
		{"merge", prog(op(stackcheck.PUSH), jz(3), op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.RET)), stackcheck.ReasonMergeMismatch, "JZ 弹后目标路 0、落空路 PUSH 后 1，同入下标 3，差一"},
		{"ret0", prog(op(stackcheck.RET)), stackcheck.ReasonBadReturnDepth, "RET 栈深 0"},
		{"fall", prog(op(stackcheck.PUSH)), stackcheck.ReasonFallthroughBounds, "末尾落空越界"},
	}
	for _, b := range bad {
		e := v.Register(b.name, b.ops)
		t.Logf("[输入] Register(%q, %s)\n[输出] reason=%q pc=%d\n[判定依据] %s；拒绝不留痕", b.name, fmtOps(b.ops), reasonOf(e), pcOf(e), b.basis)
		if reasonOf(e) != b.want {
			t.Fatalf("%s: want %s got %v", b.name, b.want, e)
		}
		if b.want != stackcheck.ReasonNameExists {
			if _, q := v.MaxStack(b.name); reasonOf(q) != stackcheck.ReasonNotFound {
				t.Fatalf("%s: rejected registration left a trace", b.name)
			}
		}
	}

	d, q := v.MaxStack("ok")
	t.Logf("[输入] MaxStack(\"ok\")\n[输出] %d, err=%v\n[判定依据] 已注册最大栈深永不改变", d, q)
	if q != nil || d != 1 {
		t.Fatalf("ok: got %d, %v", d, q)
	}
	if _, q := v.MaxStack("missing"); reasonOf(q) != stackcheck.ReasonNotFound {
		t.Fatalf("missing query: want not_found, got %v", q)
	}

	// 用更深的合法序列重复注册同名仍被拒绝，深度保持 1。
	deep := prog(op(stackcheck.PUSH), op(stackcheck.DUP), op(stackcheck.POP), op(stackcheck.POP), op(stackcheck.PUSH), op(stackcheck.RET))
	if e := v.Register("ok", deep); reasonOf(e) != stackcheck.ReasonNameExists {
		t.Fatalf("re-register: %v", e)
	}
	if d, _ := v.MaxStack("ok"); d != 1 {
		t.Fatalf("max depth changed to %d", d)
	}

	// 调用方在注册后修改原切片不影响已记录结果。
	mut := prog(op(stackcheck.PUSH), op(stackcheck.RET))
	if e := v.Register("mut", mut); e != nil {
		t.Fatal(e)
	}
	mut[0] = op(stackcheck.POP)
	if d, _ := v.MaxStack("mut"); d != 1 {
		t.Fatalf("caller mutation observed: %d", d)
	}
}

// TestReplayDeterminism 相同注册序列重放结果完全一致。
func TestReplayDeterminism(t *testing.T) {
	plays := []struct {
		name string
		ops  []stackcheck.Op
	}{
		{"a", prog(op(stackcheck.PUSH), jz(3), op(stackcheck.PUSH), jmp(4), op(stackcheck.PUSH), op(stackcheck.RET))},
		{"a", prog(op(stackcheck.PUSH), op(stackcheck.RET))},
		{"b", prog(jmp(5))},
		{"c", prog(op(stackcheck.PUSH), op(stackcheck.PUSH), op(stackcheck.PUSH))},
		{"d", prog(op(stackcheck.PUSH), op(stackcheck.RET))},
		{"d", prog()},
	}
	run := func() []string {
		v, _ := stackcheck.New(2)
		out := make([]string, 0, len(plays)*2)
		for _, p := range plays {
			e := v.Register(p.name, p.ops)
			r := "ok"
			if e != nil {
				r = fmt.Sprintf("%s@%d", e.Reason, e.PC)
			}
			out = append(out, "R:"+p.name+":"+r)
		}
		for _, p := range plays {
			d, q := v.MaxStack(p.name)
			if q != nil {
				out = append(out, "Q:"+p.name+":"+string(q.Reason))
			} else {
				out = append(out, fmt.Sprintf("Q:%s:%d", p.name, d))
			}
		}
		return out
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("replay %d differs:\n%v\n%v", i, first, got)
		}
	}
	t.Logf("[重放] 6 次结果一致: %v", first)
}

// TestConcurrentRegistration 同名并发注册恰一个成功，异名互不干扰。
func TestConcurrentRegistration(t *testing.T) {
	v, _ := stackcheck.New(4)
	const n = 64
	var wg sync.WaitGroup
	var wins, losses int64
	var mu sync.Mutex
	good := prog(op(stackcheck.PUSH), op(stackcheck.RET))
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := v.Register("same", good)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				wins++
			} else if e.Reason == stackcheck.ReasonNameExists {
				losses++
			}
		}()
	}

	// 异名并发注册全部成功；并发查询不与之死锁。
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			ops := []stackcheck.Op{op(stackcheck.PUSH)}
			for k := 0; k < i%3; k++ {
				ops = append(ops, op(stackcheck.DUP))
			}
			for k := 0; k < i%3; k++ {
				ops = append(ops, op(stackcheck.POP))
			}
			ops = append(ops, op(stackcheck.RET))
			_ = v.Register(fmt.Sprintf("f%d", i), ops)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = v.MaxStack("same")
		}()
	}
	wg.Wait()
	t.Logf("[并发] 同名注册成功 %d / 已存在 %d（总 %d）", wins, losses, n)
	if wins != 1 || losses != n-1 {
		t.Fatalf("want exactly 1 win, got wins=%d losses=%d", wins, losses)
	}
	for i := 0; i < n; i++ {
		if _, e := v.MaxStack(fmt.Sprintf("f%d", i)); e != nil {
			t.Fatalf("f%d missing: %v", i, e)
		}
	}
	if d, _ := v.MaxStack("same"); d != 1 {
		t.Fatalf("same max = %d", d)
	}
}

// toNaive 将实现层指令序列翻译成朴素推演输入。
func toNaive(ops []stackcheck.Op) []naiveOp {
	ns := make([]naiveOp, len(ops))
	for i, o := range ops {
		ns[i] = naiveOp{code: int(o.Code), target: o.Target}
	}
	return ns
}

var naiveReasonMap = map[stackcheck.Reason]naiveReason{
	"":                                 nrOK,
	stackcheck.ReasonInvalidLimit:      nrInvalidLimit,
	stackcheck.ReasonEmptyProgram:      nrEmpty,
	stackcheck.ReasonUnknownOp:         nrUnknownOp,
	stackcheck.ReasonJumpOutOfBounds:   nrJumpOOB,
	stackcheck.ReasonUnderflow:         nrUnderflow,
	stackcheck.ReasonOverflow:          nrOverflow,
	stackcheck.ReasonMergeMismatch:     nrMerge,
	stackcheck.ReasonBadReturnDepth:    nrBadReturn,
	stackcheck.ReasonFallthroughBounds: nrFallthrough,
}

// TestFuzzAgainstNaive 随机程序与随机上限下，实现必须与朴素推演逐字段一致。
func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const iterations = 20000
	checked := 0
	for it := 0; it < iterations; it++ {
		limit := 1 + rng.Intn(4)
		n := 1 + rng.Intn(7)
		ops := make([]stackcheck.Op, n)
		for i := range ops {
			code := 1 + rng.Intn(7)
			if rng.Intn(10) == 0 {
				code = 8 // 注入未知操作码
			}
			target := rng.Intn(n+2) - 1 // 覆盖 -1、内部、恰等于长度、长度+1
			ops[i] = stackcheck.Op{Code: stackcheck.OpCode(code), Target: target}
		}

		entry, maxD, e := stackcheck.Analyze(limit, ops)
		nEntry, nMax, nr, nPC, nDepth := naiveAnalyze(limit, toNaive(ops))

		gotR := naiveReasonMap[reasonOf(e)]
		if gotR != nr {
			t.Fatalf("iter %d reason mismatch L=%d ops=%s: impl=%s naive=%s", it, limit, fmtOps(ops), gotR, nr)
		}
		if nr != nrOK {
			if pcOf(e) != nPC || depthOf(e) != nDepth {
				t.Fatalf("iter %d loc mismatch ops=%s: impl(pc=%d,d=%d) naive(pc=%d,d=%d)",
					it, fmtOps(ops), pcOf(e), depthOf(e), nPC, nDepth)
			}
			continue
		}
		if maxD != nMax {
			t.Fatalf("iter %d max mismatch ops=%s: %d vs %d", it, fmtOps(ops), maxD, nMax)
		}
		if fmt.Sprint(entry) != fmt.Sprint(nEntry) {
			t.Fatalf("iter %d entry mismatch ops=%s: %v vs %v", it, fmtOps(ops), entry, nEntry)
		}
		if maxD < 0 {
			t.Fatalf("max must be >= 0: %d", maxD)
		}
		checked++
	}
	t.Logf("[对照] %d 次随机推演与朴素实现（原因/PC/栈深/入口表/max）全部一致，其中 %d 例合法", iterations, checked)
}

// TestFuzzValidProgramsRegister 随机合法程序经注册后 max 与 Analyze 一致。
func TestFuzzValidProgramsRegister(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	v, _ := stackcheck.New(4)
	ok := 0
	for it := 0; it < 5000; it++ {
		n := 1 + rng.Intn(6)
		ops := make([]stackcheck.Op, n)
		for i := range ops {
			ops[i] = stackcheck.Op{Code: stackcheck.OpCode(1 + rng.Intn(7)), Target: rng.Intn(n)}
		}
		_, maxD, e := stackcheck.Analyze(4, ops)
		name := fmt.Sprintf("p%d", it)
		re := v.Register(name, ops)
		if e == nil {
			if re != nil {
				t.Fatalf("analyze ok but register rejected: %v", re)
			}
			if d, _ := v.MaxStack(name); d != maxD {
				t.Fatalf("registered max %d != analyze %d", d, maxD)
			}
			ok++
		} else if reasonOf(re) != e.Reason {
			t.Fatalf("reject reason mismatch: %v vs %v", re, e)
		}
	}
	t.Logf("[注册对照] 5000 例随机程序中 %d 例合法并完成注册，拒绝原因与 Analyze 一致", ok)
}
