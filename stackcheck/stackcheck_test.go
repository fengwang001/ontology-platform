package stackcheck

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func kindOf(err error) ErrorKind {
	if err == nil {
		return 0
	}
	e, ok := err.(*Error)
	if !ok {
		return -1
	}
	return e.Kind
}

func mustValidator(t *testing.T, limit int) *Validator {
	t.Helper()
	v, err := NewValidator(limit)
	if err != nil {
		t.Fatalf("NewValidator(%d) 失败: %v", limit, err)
	}
	return v
}

func logCase(t *testing.T, limit int, name string, code []Instr, err error, why string) {
	t.Helper()
	t.Logf("输入: L=%d 函数=%q 指令=%v", limit, name, code)
	t.Logf("输出: err=%v", err)
	t.Logf("判定依据: %s", why)
}

func TestNewValidatorLimit(t *testing.T) {
	for _, limit := range []int{0, -1, -100} {
		_, err := NewValidator(limit)
		logCase(t, limit, "-", nil, err, "构造时栈上限小于 1 必须报 KindLimitTooSmall")
		if kindOf(err) != KindLimitTooSmall {
			t.Fatalf("L=%d: 期望 KindLimitTooSmall, 得到 %v", limit, err)
		}
	}
	if _, err := NewValidator(1); err != nil {
		t.Fatalf("L=1 应合法: %v", err)
	}
}

func TestRegisterStaticErrorPriority(t *testing.T) {
	v := mustValidator(t, 4)
	good := []Instr{{Op: OpPush}, {Op: OpRet}}
	if err := v.Register("f", good); err != nil {
		t.Fatalf("注册 f 失败: %v", err)
	}

	// 1. 名字已存在优先于一切其它错误。
	err := v.Register("f", nil)
	logCase(t, 4, "f", nil, err, "名字已存在应先于空指令序列报告")
	if kindOf(err) != KindNameExists {
		t.Fatalf("期望 KindNameExists, 得到 %v", err)
	}

	// 2. 空指令序列。
	err = v.Register("g", nil)
	logCase(t, 4, "g", nil, err, "空指令序列报 KindEmptyCode")
	if kindOf(err) != KindEmptyCode {
		t.Fatalf("期望 KindEmptyCode, 得到 %v", err)
	}

	// 3. 未知操作码先于跳转目标越界，且报最小下标。
	code := []Instr{
		{Op: OpJmp, Arg: 99}, // 下标 0：目标越界
		{Op: Op(42)},         // 下标 1：未知操作码
		{Op: Op(7)},          // 下标 2：未知操作码
	}
	err = v.Register("h", code)
	logCase(t, 4, "h", code, err, "存在未知操作码时先于任何跳转越界报告，且取最小下标 1")
	if e, _ := err.(*Error); kindOf(err) != KindUnknownOpcode || e.Index != 1 {
		t.Fatalf("期望 KindUnknownOpcode@1, 得到 %v", err)
	}

	// 4. 跳转目标越界报最小下标。
	code = []Instr{
		{Op: OpJmp, Arg: 1},
		{Op: OpJz, Arg: 3}, // 越界（len=3，合法目标 0..2）
		{Op: OpJmp, Arg: -1},
	}
	err = v.Register("i", code)
	logCase(t, 4, "i", code, err, "无未知操作码时报首个跳转目标越界（下标 1）")
	if e, _ := err.(*Error); kindOf(err) != KindJumpTargetOutOfRange || e.Index != 1 {
		t.Fatalf("期望 KindJumpTargetOutOfRange@1, 得到 %v", err)
	}

	// 被拒绝的注册不留痕迹。
	for _, name := range []string{"g", "h", "i"} {
		if _, qerr := v.MaxDepth(name); kindOf(qerr) != KindFunctionNotFound {
			t.Fatalf("被拒绝的注册 %q 不应留痕, 得到 %v", name, qerr)
		}
	}
	if d, qerr := v.MaxDepth("f"); qerr != nil || d != 1 {
		t.Fatalf("已注册函数 f 应不受影响: d=%d err=%v", d, qerr)
	}
}

func TestQueryMissingFunction(t *testing.T) {
	v := mustValidator(t, 2)
	_, err := v.MaxDepth("nope")
	logCase(t, 2, "nope", nil, err, "查询不存在的函数报 KindFunctionNotFound")
	if kindOf(err) != KindFunctionNotFound {
		t.Fatalf("期望 KindFunctionNotFound, 得到 %v", err)
	}
}

func TestDepthExactlyLimitAndLimitPlusOne(t *testing.T) {
	// 栈深恰为 L：通过，最大栈深 == L。
	v := mustValidator(t, 2)
	code := []Instr{{Op: OpPush}, {Op: OpPush}, {Op: OpAdd}, {Op: OpRet}}
	err := v.Register("exact", code)
	logCase(t, 2, "exact", code, err, "峰值栈深恰为 L=2（恰等于允许），最大栈深记 2")
	if err != nil {
		t.Fatalf("恰为 L 应通过: %v", err)
	}
	if d, _ := v.MaxDepth("exact"); d != 2 {
		t.Fatalf("最大栈深应为 2, 得到 %d", d)
	}

	// 栈深 L+1：超限。
	code = []Instr{{Op: OpPush}, {Op: OpPush}, {Op: OpPush}, {Op: OpAdd}, {Op: OpAdd}, {Op: OpRet}}
	err = v.Register("over", code)
	logCase(t, 2, "over", code, err, "第三条 PUSH 执行后栈深 3 > L=2，报 KindOverflow")
	if e, _ := err.(*Error); kindOf(err) != KindOverflow || e.Index != 2 || e.Depth != 3 {
		t.Fatalf("期望 KindOverflow@2 深度 3, 得到 %v", err)
	}
	if _, qerr := v.MaxDepth("over"); kindOf(qerr) != KindFunctionNotFound {
		t.Fatalf("超限注册不应留痕, 得到 %v", qerr)
	}
}

func TestMergeConsistentAndOffByOne(t *testing.T) {
	// 汇合恰一致：JZ 两路以相同栈深到达下标 3。
	v := mustValidator(t, 4)
	code := []Instr{
		{Op: OpPush},       // 0: 0->1
		{Op: OpJz, Arg: 3}, // 1: 弹后 0，目标 3 与落空 2 均以 0 进入
		{Op: OpPush},       // 2: 0->1，使下标 3 汇合到 1
		{Op: OpPush},       // 3: JZ 目标路栈深 0、经下标 2 路栈深 1，差一
		{Op: OpRet},        // 4
	}
	// 下标 3 处两路栈深为 0（JZ 目标）与 1（经下标 2 的 PUSH），不一致；
	// 先验证差一被拒，再验证恰一致通过。
	err := v.Register("mismatch", code)
	logCase(t, 4, "mismatch", code, err, "JZ 目标路栈深 0、落空经 PUSH 路栈深 1，汇合差一报 KindMergeMismatch@3")
	if e, _ := err.(*Error); kindOf(err) != KindMergeMismatch || e.Index != 3 {
		t.Fatalf("期望 KindMergeMismatch@3, 得到 %v", err)
	}

	consistent := []Instr{
		{Op: OpPush},       // 0: 0->1
		{Op: OpJz, Arg: 3}, // 1: 弹后 0，两路均以 0 进入下标 3
		{Op: OpJmp, Arg: 3},
		{Op: OpPush}, // 3: 0->1
		{Op: OpRet},  // 4: 恰为 1
	}
	err = v.Register("consistent", consistent)
	logCase(t, 4, "consistent", consistent, err, "JZ 两路均以栈深 0 汇合于下标 3，恰一致通过")
	if err != nil {
		t.Fatalf("汇合恰一致应通过: %v", err)
	}
	if d, _ := v.MaxDepth("consistent"); d != 1 {
		t.Fatalf("最大栈深应为 1, 得到 %d", d)
	}
}

func TestRetDepth(t *testing.T) {
	for _, tc := range []struct {
		depth int
		code  []Instr
	}{
		{0, []Instr{{Op: OpRet}}},
		{1, []Instr{{Op: OpPush}, {Op: OpRet}}},
		{2, []Instr{{Op: OpPush}, {Op: OpPush}, {Op: OpRet}}},
	} {
		v := mustValidator(t, 4)
		name := fmt.Sprintf("ret%d", tc.depth)
		err := v.Register(name, tc.code)
		logCase(t, 4, name, tc.code, err, fmt.Sprintf("RET 入口栈深 %d：仅恰为 1 时通过", tc.depth))
		if tc.depth == 1 {
			if err != nil {
				t.Fatalf("RET 栈深 1 应通过: %v", err)
			}
			continue
		}
		if kindOf(err) != KindRetDepth {
			t.Fatalf("RET 栈深 %d 应报 KindRetDepth, 得到 %v", tc.depth, err)
		}
	}
}

func TestJzPopThenMerge(t *testing.T) {
	// JZ 先弹一个再分两路：两路都以弹后栈深进入后继。
	v := mustValidator(t, 4)
	code := []Instr{
		{Op: OpPush},       // 0: 0->1
		{Op: OpPush},       // 1: 1->2
		{Op: OpJz, Arg: 4}, // 2: 弹后 1，目标 4 与落空 3 均以 1 进入
		{Op: OpAdd},        // 3: 入口 1 < 2 —— 若 JZ 不弹则会以 2 进入而不报错
		{Op: OpRet},        // 4: 入口 1，合法
	}
	err := v.Register("jzpop", code)
	logCase(t, 4, "jzpop", code, err, "JZ 弹后栈深 1 进入落空路下标 3，ADD 入口不足报 KindUnderflow@3")
	if e, _ := err.(*Error); kindOf(err) != KindUnderflow || e.Index != 3 {
		t.Fatalf("期望 KindUnderflow@3, 得到 %v", err)
	}

	// 修正落空路使其与弹后栈深匹配：两路汇合于 RET。
	ok := []Instr{
		{Op: OpPush},       // 0: 0->1
		{Op: OpPush},       // 1: 1->2
		{Op: OpJz, Arg: 5}, // 2: 弹后 1
		{Op: OpDup},        // 3: 1->2
		{Op: OpAdd},        // 4: 2->1
		{Op: OpRet},        // 5: 两路均以 1 进入
	}
	err = v.Register("jzok", ok)
	logCase(t, 4, "jzok", ok, err, "JZ 弹后两路分别以 1（目标）与 1（经 DUP+ADD）汇合于 RET，通过")
	if err != nil {
		t.Fatalf("JZ 弹后汇合一致应通过: %v", err)
	}
}

func TestUnreachableCode(t *testing.T) {
	// 不可达指令的下溢不报。
	v := mustValidator(t, 4)
	code := []Instr{
		{Op: OpPush}, // 0: 0->1
		{Op: OpRet},  // 1: 返回，下标 2 起不可达
		{Op: OpPop},  // 2: 不可达，若在深度 0 执行会下溢，但不应检查
		{Op: OpAdd},  // 3: 不可达，同样不检查
	}
	err := v.Register("unreach", code)
	logCase(t, 4, "unreach", code, err, "不可达指令不受栈约束检查，下溢不报，注册应成功")
	if err != nil {
		t.Fatalf("不可达下溢不应报: %v", err)
	}
	if d, _ := v.MaxDepth("unreach"); d != 1 {
		t.Fatalf("最大栈深只统计可达指令，应为 1, 得到 %d", d)
	}

	// 不可达指令里的越界跳转必报。
	code = []Instr{
		{Op: OpPush},
		{Op: OpRet},
		{Op: OpJmp, Arg: 4}, // 不可达，但目标 4 == len 越界
	}
	err = v.Register("unreachjmp", code)
	logCase(t, 4, "unreachjmp", code, err, "跳转目标范围对全部指令（含不可达）检查，报 KindJumpTargetOutOfRange@2")
	if e, _ := err.(*Error); kindOf(err) != KindJumpTargetOutOfRange || e.Index != 2 {
		t.Fatalf("期望 KindJumpTargetOutOfRange@2, 得到 %v", err)
	}
}

func TestJumpTargetEqualsLength(t *testing.T) {
	v := mustValidator(t, 4)
	code := []Instr{
		{Op: OpPush},
		{Op: OpJz, Arg: 2}, // len=2，目标恰等于长度，非法
	}
	err := v.Register("eq", code)
	logCase(t, 4, "eq", code, err, "合法目标为 0..len-1，目标恰等于长度必须拒绝")
	if kindOf(err) != KindJumpTargetOutOfRange {
		t.Fatalf("期望 KindJumpTargetOutOfRange, 得到 %v", err)
	}
}

func TestLoopStackDrift(t *testing.T) {
	v := mustValidator(t, 4)
	code := []Instr{
		{Op: OpPush},        // 0: 入口 0，执行后 1
		{Op: OpJmp, Arg: 0}, // 1: 以栈深 1 回到下标 0，与已有入口 0 冲突
	}
	err := v.Register("loop", code)
	logCase(t, 4, "loop", code, err, "环路每圈栈深 +1 漂移，回到下标 0 时 1 != 0，报 KindMergeMismatch@0")
	if e, _ := err.(*Error); kindOf(err) != KindMergeMismatch || e.Index != 0 {
		t.Fatalf("期望 KindMergeMismatch@0, 得到 %v", err)
	}

	// 栈深守恒的环路则合法。
	ok := []Instr{
		{Op: OpPush},        // 0: 0->1
		{Op: OpJz, Arg: 4},  // 1: 弹后 0，目标 4 以 0 进入
		{Op: OpPush},        // 2: 0->1
		{Op: OpJmp, Arg: 1}, // 3: 以 1 回到下标 1，与已有入口 1 一致
		{Op: OpPush},        // 4: 0->1
		{Op: OpRet},         // 5: 恰为 1
	}
	err = v.Register("loopok", ok)
	logCase(t, 4, "loopok", ok, err, "环路回到下标 1 时栈深均为 1，汇合一致，通过")
	if err != nil {
		t.Fatalf("栈深守恒环路应通过: %v", err)
	}
}

func TestFallthroughOutOfRange(t *testing.T) {
	v := mustValidator(t, 4)
	code := []Instr{{Op: OpPush}} // 落空到下标 1 == len
	err := v.Register("ft", code)
	logCase(t, 4, "ft", code, err, "PUSH 落空到下标等于指令总数处，报 KindFallthroughOutOfRange@0")
	if e, _ := err.(*Error); kindOf(err) != KindFallthroughOutOfRange || e.Index != 0 {
		t.Fatalf("期望 KindFallthroughOutOfRange@0, 得到 %v", err)
	}

	code = []Instr{
		{Op: OpPush},
		{Op: OpJz, Arg: 0}, // 落空到下标 2 == len
	}
	err = v.Register("ftjz", code)
	logCase(t, 4, "ftjz", code, err, "JZ 落空路越界同样报 KindFallthroughOutOfRange@1")
	if e, _ := err.(*Error); kindOf(err) != KindFallthroughOutOfRange || e.Index != 1 {
		t.Fatalf("期望 KindFallthroughOutOfRange@1, 得到 %v", err)
	}
}

func TestUnderflowBeforeOverflowAndRet(t *testing.T) {
	// 同一条指令内先下溢：POP 入口 0。
	v := mustValidator(t, 1)
	code := []Instr{
		{Op: OpJz, Arg: 2}, // 0: 入口 0，JZ 先弹 -> 下溢
		{Op: OpRet},
		{Op: OpRet},
	}
	err := v.Register("u1", code)
	logCase(t, 1, "u1", code, err, "JZ 入口栈深 0，先报下溢而非其它")
	if kindOf(err) != KindUnderflow {
		t.Fatalf("期望 KindUnderflow, 得到 %v", err)
	}
}

func TestConcurrentRegisterSameName(t *testing.T) {
	v := mustValidator(t, 4)
	code := []Instr{{Op: OpPush}, {Op: OpRet}}
	const n = 32
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- v.Register("hot", code)
		}()
	}
	wg.Wait()
	close(results)
	okCount := 0
	for err := range results {
		if err == nil {
			okCount++
		} else if kindOf(err) != KindNameExists {
			t.Fatalf("并发同名注册只能成功或报名字已存在, 得到 %v", err)
		}
	}
	t.Logf("输入: %d 个 goroutine 并发注册同名函数", n)
	t.Logf("输出: 成功 %d 个", okCount)
	t.Logf("判定依据: 同名函数并发注册恰有一个成功")
	if okCount != 1 {
		t.Fatalf("恰有一个成功, 实际 %d", okCount)
	}

	// 并发查询已注册函数，最大栈深永不改变。
	var wg2 sync.WaitGroup
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			d, err := v.MaxDepth("hot")
			if err != nil || d != 1 {
				t.Errorf("MaxDepth(hot) = %d, %v; 期望恒为 1", d, err)
			}
		}()
	}
	wg2.Wait()
}

func TestReplayDeterminism(t *testing.T) {
	programs := []struct {
		name string
		code []Instr
	}{
		{"a", []Instr{{Op: OpPush}, {Op: OpRet}}},
		{"b", []Instr{{Op: OpPop}, {Op: OpRet}}},
		{"a", []Instr{{Op: OpPush}, {Op: OpRet}}},
		{"c", []Instr{{Op: OpJmp, Arg: 9}}},
		{"b", []Instr{{Op: OpPush}, {Op: OpPush}, {Op: OpAdd}, {Op: OpRet}}},
	}
	run := func() []string {
		v := mustValidator(t, 3)
		out := []string{}
		for _, p := range programs {
			err := v.Register(p.name, p.code)
			if err == nil {
				d, _ := v.MaxDepth(p.name)
				out = append(out, fmt.Sprintf("%s:ok:%d", p.name, d))
			} else {
				out = append(out, fmt.Sprintf("%s:%s", p.name, err))
			}
		}
		return out
	}
	first := run()
	second := run()
	t.Logf("输入: 固定注册序列重放两次")
	t.Logf("输出: 第一次 %v / 第二次 %v", first, second)
	t.Logf("判定依据: 相同注册序列重放得到完全相同的结果与错误")
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放不一致: %v vs %v", first, second)
		}
	}
}

// naiveCheck 是按规则直写的朴素推演（对照实现）：
// 静态扫描 + FIFO 工作表，逐条模拟栈效应，独立实现用于交叉验证。
func naiveCheck(code []Instr, limit int) (int, ErrorKind, int) {
	n := len(code)
	if n == 0 {
		return 0, KindEmptyCode, -1
	}
	for i, in := range code {
		if in.Op < OpPush || in.Op > OpRet {
			return 0, KindUnknownOpcode, i
		}
	}
	for i, in := range code {
		if (in.Op == OpJmp || in.Op == OpJz) && (in.Arg < 0 || in.Arg >= n) {
			return 0, KindJumpTargetOutOfRange, i
		}
	}
	entry := map[int]int{0: 0}
	queue := []int{0}
	maxDepth := 0
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		d := entry[i]
		in := code[i]
		out := d
		var succs []int
		switch in.Op {
		case OpPush:
			out = d + 1
			succs = []int{i + 1}
		case OpPop:
			if d < 1 {
				return 0, KindUnderflow, i
			}
			out = d - 1
			succs = []int{i + 1}
		case OpAdd:
			if d < 2 {
				return 0, KindUnderflow, i
			}
			out = d - 1
			succs = []int{i + 1}
		case OpDup:
			if d < 1 {
				return 0, KindUnderflow, i
			}
			out = d + 1
			succs = []int{i + 1}
		case OpJmp:
			succs = []int{in.Arg}
		case OpJz:
			if d < 1 {
				return 0, KindUnderflow, i
			}
			out = d - 1
			succs = []int{in.Arg, i + 1}
		case OpRet:
			if d != 1 {
				return 0, KindRetDepth, i
			}
			out = 0
		}
		if out > limit {
			return 0, KindOverflow, i
		}
		if out > maxDepth {
			maxDepth = out
		}
		for _, s := range succs {
			if s == n {
				return 0, KindFallthroughOutOfRange, i
			}
			if old, seen := entry[s]; seen {
				if old != out {
					return 0, KindMergeMismatch, s
				}
				continue
			}
			entry[s] = out
			queue = append(queue, s)
		}
	}
	return maxDepth, 0, -1
}

func TestNaiveCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const cases = 3000
	logged := 0
	for c := 0; c < cases; c++ {
		n := 1 + rng.Intn(10)
		limit := 1 + rng.Intn(4)
		code := make([]Instr, n)
		for i := range code {
			op := Op(rng.Intn(9)) // 0..6 合法，7/8 为未知操作码
			arg := 0
			if op == OpJmp || op == OpJz {
				arg = rng.Intn(n+2) - 1 // 覆盖 -1、0..n-1、n（越界）
			}
			code[i] = Instr{Op: op, Arg: arg}
		}
		wantDepth, wantKind, wantIndex := naiveCheck(code, limit)

		v := mustValidator(t, limit)
		err := v.Register("f", code)
		gotKind := kindOf(err)
		gotIndex := -1
		if e, ok := err.(*Error); ok {
			gotIndex = e.Index
		}
		gotDepth := -1
		if err == nil {
			gotDepth, _ = v.MaxDepth("f")
		}

		if logged < 5 {
			logged++
			t.Logf("输入: L=%d 指令=%v", limit, code)
			t.Logf("输出: 被测(kind=%d idx=%d depth=%d) 朴素(kind=%d idx=%d depth=%d)",
				gotKind, gotIndex, gotDepth, wantKind, wantIndex, wantDepth)
			t.Logf("判定依据: 与按规则直写的朴素推演逐点对照，结果须完全一致")
		}

		if gotKind != wantKind || gotIndex != wantIndex {
			t.Fatalf("用例 %d 不一致: 被测(kind=%d idx=%d) 朴素(kind=%d idx=%d) 指令=%v L=%d",
				c, gotKind, gotIndex, wantKind, wantIndex, code, limit)
		}
		if wantKind == 0 && gotDepth != wantDepth {
			t.Fatalf("用例 %d 最大栈深不一致: 被测 %d 朴素 %d 指令=%v L=%d",
				c, gotDepth, wantDepth, code, limit)
		}
		// 失败注册不留痕。
		if wantKind != 0 {
			if _, qerr := v.MaxDepth("f"); kindOf(qerr) != KindFunctionNotFound {
				t.Fatalf("用例 %d 失败注册留痕", c)
			}
		}
	}
}
