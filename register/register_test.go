package register

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// describe 把一条操作记录格式化为日志文本。
func describe(rec opRecord) string {
	var op string
	switch rec.op.Kind {
	case Write:
		op = fmt.Sprintf("write(%d)", rec.op.V)
	case Read:
		op = "read()"
	case CAS:
		op = fmt.Sprintf("cas(e=%d,n=%d)", rec.op.E, rec.op.N)
	}
	if !rec.finished {
		return fmt.Sprintf("#%d %s invoke=%d 未结束", rec.id, op, rec.invoke)
	}
	switch rec.op.Kind {
	case Read:
		return fmt.Sprintf("#%d %s invoke=%d ret=%d -> %d", rec.id, op, rec.invoke, rec.ret, *rec.res.Value)
	case CAS:
		return fmt.Sprintf("#%d %s invoke=%d ret=%d -> success=%v", rec.id, op, rec.invoke, rec.ret, *rec.res.Success)
	}
	return fmt.Sprintf("#%d %s invoke=%d ret=%d", rec.id, op, rec.invoke, rec.ret)
}

// logHistory 在测试日志中打印输入历史。
func logHistory(t *testing.T, c *Checker) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Log("输入历史:")
	for _, rec := range c.ops {
		t.Logf("  %s", describe(*rec))
	}
}

// logVerdict 在测试日志中打印输出与判定依据。
func logVerdict(t *testing.T, c *Checker, ok bool, witness []int) {
	t.Helper()
	if !ok {
		t.Log("输出: 不可线性化（不存在同时满足实时先后与寄存器语义的全序）")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	value := 0
	fmt.Fprintf(&b, "初值 0")
	for _, id := range witness {
		rec := c.ops[id]
		next, _ := apply(*rec, value)
		fmt.Fprintf(&b, " -> [#%d %s] 值变为 %d", id, strings.Split(describe(*rec), " ")[1], next)
		value = next
	}
	t.Logf("输出: 可线性化，见证序 %v", witness)
	t.Logf("判定依据: %s", b.String())
}

// verifyWitness 校验见证序确实满足全部判定条件。
func verifyWitness(t *testing.T, c *Checker, witness []int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	placed := make(map[int]bool, len(witness))
	for _, id := range witness {
		if id < 0 || id >= len(c.ops) {
			t.Fatalf("见证序包含非法编号 %d", id)
		}
		if placed[id] {
			t.Fatalf("见证序中编号 %d 重复", id)
		}
		placed[id] = true
	}
	for _, rec := range c.ops {
		if rec.finished && !placed[rec.id] {
			t.Fatalf("见证序缺少已结束操作 #%d", rec.id)
		}
		if !rec.finished && placed[rec.id] && rec.op.Kind == Read {
			t.Fatalf("见证序包含未结束的读 #%d", rec.id)
		}
	}
	pos := make(map[int]int, len(witness))
	for i, id := range witness {
		pos[id] = i
	}
	for _, a := range c.ops {
		if !a.finished || !placed[a.id] {
			continue
		}
		for _, b := range c.ops {
			if placed[b.id] && a.ret < b.invoke && pos[a.id] > pos[b.id] {
				t.Fatalf("见证序违反实时先后: #%d(ret=%d) 应在 #%d(invoke=%d) 前", a.id, a.ret, b.id, b.invoke)
			}
		}
	}
	value := 0
	for _, id := range witness {
		next, ok := apply(*c.ops[id], value)
		if !ok {
			t.Fatalf("见证序中 #%d 在当前值 %d 下不满足寄存器语义", id, value)
		}
		value = next
	}
}

// runCheck 执行检查并打印日志、校验见证。
func runCheck(t *testing.T, c *Checker) (bool, []int) {
	t.Helper()
	logHistory(t, c)
	ok, witness := c.Check()
	logVerdict(t, c, ok, witness)
	if ok {
		verifyWitness(t, c, witness)
	}
	// 相同历史反复检查结论与见证序必须一致。
	for i := 0; i < 3; i++ {
		ok2, witness2 := c.Check()
		if ok2 != ok || !reflect.DeepEqual(witness2, witness) {
			t.Fatalf("重复检查不一致: (%v,%v) vs (%v,%v)", ok, witness, ok2, witness2)
		}
	}
	return ok, witness
}

func mustBegin(t *testing.T, c *Checker, client string, op Operation, invoke int) int {
	t.Helper()
	id, err := c.Begin(client, op, invoke)
	if err != nil {
		t.Fatalf("Begin(%q, %+v, %d) 意外失败: %v", client, op, invoke, err)
	}
	return id
}

func mustEnd(t *testing.T, c *Checker, id, ret int, res Result) {
	t.Helper()
	if err := c.End(id, ret, res); err != nil {
		t.Fatalf("End(%d, %d, %+v) 意外失败: %v", id, ret, res, err)
	}
}

// TestFailedCASMustObserveDifferentValue 失败的 CAS 必须观察到不等于期望值的当前值。
func TestFailedCASMustObserveDifferentValue(t *testing.T) {
	c := NewChecker()
	w := mustBegin(t, c, "a", NewWrite(1), 0)
	mustEnd(t, c, w, 1, WriteResult())
	cas := mustBegin(t, c, "b", NewCAS(1, 5), 2)
	mustEnd(t, c, cas, 3, CASResult(false))
	r := mustBegin(t, c, "c", NewRead(), 4)
	mustEnd(t, c, r, 5, ReadResult(1))

	ok, _ := runCheck(t, c)
	if ok {
		t.Fatal("写 1 之后失败的 CAS(e=1) 与读 1 不应可线性化")
	}

	// 对照：失败 CAS 的期望值不等于当前值时可线性化。
	c2 := NewChecker()
	w = mustBegin(t, c2, "a", NewWrite(1), 0)
	mustEnd(t, c2, w, 1, WriteResult())
	cas = mustBegin(t, c2, "b", NewCAS(2, 5), 2)
	mustEnd(t, c2, cas, 3, CASResult(false))
	r = mustBegin(t, c2, "c", NewRead(), 4)
	mustEnd(t, c2, r, 5, ReadResult(1))

	ok, _ = runCheck(t, c2)
	if !ok {
		t.Fatal("写 1 之后失败的 CAS(e=2) 与读 1 应可线性化")
	}
}

// TestEqualTimesAreConcurrent 调用与返回时刻恰相等按并发处理。
func TestEqualTimesAreConcurrent(t *testing.T) {
	c := NewChecker()
	w := mustBegin(t, c, "a", NewWrite(1), 0)
	mustEnd(t, c, w, 5, WriteResult())
	r := mustBegin(t, c, "b", NewRead(), 5) // 调用时刻恰等于写的返回时刻
	mustEnd(t, c, r, 6, ReadResult(0))

	ok, witness := runCheck(t, c)
	if !ok {
		t.Fatal("返回时刻等于调用时刻应视为并发，历史应可线性化")
	}
	// 见证序按（调用时刻，编号）升序首个成功：读(#1, invoke=5) 在写(#0, invoke=0) 后尝试，
	// 但写先放会导致读 0 失败，故首个成功序为 [1, 0]。
	if !reflect.DeepEqual(witness, []int{1, 0}) {
		t.Fatalf("见证序应为 [1 0]，实际 %v", witness)
	}
}

// TestUnfinishedWriteExplainsRead 未结束的写可放入并解释他人读到的值。
func TestUnfinishedWriteExplainsRead(t *testing.T) {
	c := NewChecker()
	mustBegin(t, c, "a", NewWrite(7), 0) // 未结束
	r := mustBegin(t, c, "b", NewRead(), 1)
	mustEnd(t, c, r, 2, ReadResult(7))

	ok, _ := runCheck(t, c)
	if !ok {
		t.Fatal("未结束的写应能解释读到的 7")
	}
}

// TestUnfinishedCAS 未结束的 CAS 仅在当前值等于期望值时可放入并生效。
func TestUnfinishedCAS(t *testing.T) {
	// 当前值 0 等于期望值，未结束 CAS 生效，解释读到的 3。
	c := NewChecker()
	mustBegin(t, c, "a", NewCAS(0, 3), 0) // 未结束
	r := mustBegin(t, c, "b", NewRead(), 1)
	mustEnd(t, c, r, 2, ReadResult(3))

	ok, _ := runCheck(t, c)
	if !ok {
		t.Fatal("当前值等于期望值时未结束 CAS 应能生效")
	}

	// 实时约束迫使写 1 在读之前；未结束 CAS(e=0) 无论放在写前还是写后
	// 都无法同时满足自身生效条件与读到的值，故不可线性化。
	c2 := NewChecker()
	w := mustBegin(t, c2, "a", NewWrite(1), 0)
	mustEnd(t, c2, w, 1, WriteResult())
	mustBegin(t, c2, "b", NewCAS(0, 3), 2) // 未结束
	r = mustBegin(t, c2, "c", NewRead(), 3)
	mustEnd(t, c2, r, 4, ReadResult(3))

	ok, _ = runCheck(t, c2)
	if ok {
		t.Fatal("未结束 CAS(e=0) 在当前值 1 下不应生效，历史不可线性化")
	}
}

// TestBeginValidation 开始校验按顺序只报第一个错误，且拒绝不改变状态。
func TestBeginValidation(t *testing.T) {
	c := NewChecker()
	id := mustBegin(t, c, "a", NewWrite(1), 5)

	// 客户端已有未结束操作。
	if _, err := c.Begin("a", NewRead(), 6); err == nil ||
		!strings.Contains(err.Error(), "unfinished") {
		t.Fatalf("应报告客户端已有未结束操作，实际 %v", err)
	}
	mustEnd(t, c, id, 10, WriteResult())

	// 开始时刻小于该客户端上次结束时刻。
	if _, err := c.Begin("a", NewRead(), 9); err == nil ||
		!strings.Contains(err.Error(), "last finish time") {
		t.Fatalf("应报告开始时刻早于上次结束时刻，实际 %v", err)
	}
	// 被拒绝的操作不改变状态：相等时刻可以开始。
	id2 := mustBegin(t, c, "a", NewRead(), 10)
	mustEnd(t, c, id2, 11, ReadResult(1))

	// 操作总数超过上限。
	c2 := NewChecker()
	for i := 0; i < MaxOps; i++ {
		mustBegin(t, c2, fmt.Sprintf("cli-%d", i), NewWrite(i), i)
	}
	if _, err := c2.Begin("x", NewWrite(0), 100); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("应报告操作总数超限，实际 %v", err)
	}
	// 超限拒绝不改变状态：已有操作仍可正常结束。
	mustEnd(t, c2, 0, 100, WriteResult())
}

// TestEndValidation 结束校验按顺序只报第一个错误，且拒绝不改变状态。
func TestEndValidation(t *testing.T) {
	c := NewChecker()
	w := mustBegin(t, c, "a", NewWrite(1), 0)
	r := mustBegin(t, c, "b", NewRead(), 0)
	cas := mustBegin(t, c, "c", NewCAS(0, 1), 0)

	// 编号不存在。
	if err := c.End(99, 1, WriteResult()); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("应报告编号不存在，实际 %v", err)
	}
	// 结束时刻小于开始时刻。
	if err := c.End(w, -1, WriteResult()); err == nil ||
		!strings.Contains(err.Error(), "before invoke time") {
		t.Fatalf("应报告结束时刻早于开始时刻，实际 %v", err)
	}
	// 写带结果。
	v := 1
	if err := c.End(w, 1, Result{Value: &v}); err == nil ||
		!strings.Contains(err.Error(), "must not carry a result") {
		t.Fatalf("应报告写带结果，实际 %v", err)
	}
	// 读不带值。
	if err := c.End(r, 1, WriteResult()); err == nil ||
		!strings.Contains(err.Error(), "must carry a returned value") {
		t.Fatalf("应报告读不带值，实际 %v", err)
	}
	// 比较交换不带成功或失败。
	if err := c.End(cas, 1, WriteResult()); err == nil ||
		!strings.Contains(err.Error(), "must carry a success flag") {
		t.Fatalf("应报告 CAS 不带成功标志，实际 %v", err)
	}
	// 被拒绝的结束不改变状态：这些操作仍未结束，可以正确结束。
	mustEnd(t, c, w, 1, WriteResult())
	mustEnd(t, c, r, 1, ReadResult(1))
	mustEnd(t, c, cas, 1, CASResult(false))
	// 已结束后再次结束应报编号错误。
	if err := c.End(w, 2, WriteResult()); err == nil ||
		!strings.Contains(err.Error(), "already finished") {
		t.Fatalf("应报告操作已结束，实际 %v", err)
	}

	ok, _ := runCheck(t, c)
	if !ok {
		t.Fatal("历史应可线性化")
	}
}
