package aria

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

// mustNew 构造合法执行器，失败即终止测试。
func mustNew(t *testing.T, k, bsz int) *Executor {
	t.Helper()
	e, err := New(k, bsz)
	if err != nil {
		t.Fatalf("New(%d, %d) 意外失败: %v", k, bsz, err)
	}
	return e
}

// mustSubmit 登记事务，失败即终止测试。
func mustSubmit(t *testing.T, e *Executor, ops ...Op) uint64 {
	t.Helper()
	id, err := e.Submit(ops)
	if err != nil {
		t.Fatalf("Submit(%v) 意外被拒: %v", ops, err)
	}
	return id
}

// stateOf 读取全部 K 个键的当前值。
func stateOf(t *testing.T, e *Executor, k int) []int64 {
	t.Helper()
	s := make([]int64, k)
	for i := range s {
		v, err := e.Value(i)
		if err != nil {
			t.Fatalf("Value(%d) 意外报错: %v", i, err)
		}
		s[i] = v
	}
	return s
}

// wantRes 描述一个事务的预期结局。
type wantRes struct {
	phase Phase
	acc   int64
	ok    bool
}

// checkBatch 断言批结果与预期一致，并打印判定依据日志。
func checkBatch(t *testing.T, got []Result, firstID uint64, want []wantRes) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("批结果数=%d，预期 %d", len(got), len(want))
	}
	for i, w := range want {
		r := got[i]
		if r.TxID != firstID+uint64(i) {
			t.Errorf("结果[%d] 事务号=%d，预期 %d", i, r.TxID, firstID+uint64(i))
		}
		if r.Phase != w.phase || r.HasAcc != w.ok || (w.ok && r.Acc != w.acc) {
			t.Errorf("T%d 结局=(%s, acc=%d, ok=%v)，预期 (%s, acc=%d, ok=%v)",
				r.TxID, r.Phase, r.Acc, r.HasAcc, w.phase, w.acc, w.ok)
		}
		t.Logf("T%d 阶段=%s acc=%d ok=%v 依据: %s", r.TxID, r.Phase, r.Acc, r.HasAcc, r.Reason)
	}
}

func TestNewInvalidConfig(t *testing.T) {
	for _, c := range [][2]int{{0, 1}, {-1, 1}, {65, 1}, {1, 0}, {1, 17}, {1, -1}, {0, 0}, {100, 100}} {
		if _, err := New(c[0], c[1]); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%d, %d) err=%v，预期 ErrInvalidConfig", c[0], c[1], err)
		}
	}
	for _, c := range [][2]int{{1, 1}, {64, 16}, {1, 16}, {64, 1}} {
		if _, err := New(c[0], c[1]); err != nil {
			t.Errorf("New(%d, %d) 应合法，得到 %v", c[0], c[1], err)
		}
	}
}

func TestSubmitRejection(t *testing.T) {
	e := mustNew(t, 2, 2)
	// 操作条数不在 1 到 8。
	if _, err := e.Submit(nil); !errors.Is(err, ErrOpCount) {
		t.Errorf("空操作 err=%v，预期 ErrOpCount", err)
	}
	nine := make([]Op, 9)
	for i := range nine {
		nine[i] = R(0)
	}
	if _, err := e.Submit(nine); !errors.Is(err, ErrOpCount) {
		t.Errorf("9 条操作 err=%v，预期 ErrOpCount", err)
	}
	// 键越界。
	if _, err := e.Submit([]Op{R(2)}); !errors.Is(err, ErrKeyRange) {
		t.Errorf("键 2 err=%v，预期 ErrKeyRange", err)
	}
	if _, err := e.Submit([]Op{W(-1, 0)}); !errors.Is(err, ErrKeyRange) {
		t.Errorf("键 -1 err=%v，预期 ErrKeyRange", err)
	}
	// d 越界。
	if _, err := e.Submit([]Op{W(0, MaxDelta+1)}); !errors.Is(err, ErrDeltaRange) {
		t.Errorf("d 越界 err=%v，预期 ErrDeltaRange", err)
	}
	if _, err := e.Submit([]Op{W(0, MinDelta-1)}); !errors.Is(err, ErrDeltaRange) {
		t.Errorf("d 越界 err=%v，预期 ErrDeltaRange", err)
	}
	// 只报第一个原因：条数优先于键，键优先于 d。
	if _, err := e.Submit(append(nine, R(9))); !errors.Is(err, ErrOpCount) {
		t.Errorf("条数+键同时非法 err=%v，预期只报 ErrOpCount", err)
	}
	if _, err := e.Submit([]Op{W(9, MaxDelta+1)}); !errors.Is(err, ErrKeyRange) {
		t.Errorf("键+d 同时非法 err=%v，预期只报 ErrKeyRange", err)
	}
	// 被拒绝的调用不改变状态，也不消耗事务号。
	if e.Pending() != 0 {
		t.Errorf("拒绝后 Pending=%d，预期 0", e.Pending())
	}
	id := mustSubmit(t, e, R(0))
	if id != 1 {
		t.Errorf("拒绝消耗了事务号：首个有效事务号=%d，预期 1", id)
	}
}

func TestValueOutOfRange(t *testing.T) {
	e := mustNew(t, 2, 2)
	if _, err := e.Value(-1); !errors.Is(err, ErrKeyRange) {
		t.Errorf("Value(-1) err=%v，预期 ErrKeyRange", err)
	}
	if _, err := e.Value(2); !errors.Is(err, ErrKeyRange) {
		t.Errorf("Value(2) err=%v，预期 ErrKeyRange", err)
	}
	if got := stateOf(t, e, 2); !reflect.DeepEqual(got, []int64{0, 0}) {
		t.Errorf("Value 越界后状态被改变: %v", got)
	}
}

func TestEmptyBatch(t *testing.T) {
	e := mustNew(t, 3, 2)
	if got := e.RunBatch(); len(got) != 0 {
		t.Errorf("空批返回 %v，预期空", got)
	}
	if got := stateOf(t, e, 3); !reflect.DeepEqual(got, []int64{0, 0, 0}) {
		t.Errorf("空批改变了状态: %v", got)
	}
}

// 读到自己缓冲写入的键不计入 RS（白盒核对 execute 的读集）。
// 注：该键必在 WS 中，若被误计入 RS，则任何更早写它的事务同时造成
// WAW 与 RAW，行为上不可区分，故此处直接核对内部读集。
func TestExecuteReadOwnBufferedWriteNotInRS(t *testing.T) {
	state := []int64{7}
	tr := execute(state, []Op{W(0, 5), R(0)})
	if tr.failed {
		t.Fatal("不应溢出")
	}
	if len(tr.rs) != 0 {
		t.Errorf("读到缓冲写入的键却计入 RS: %v", tr.rs)
	}
	if !reflect.DeepEqual(tr.ws, []int{0}) {
		t.Errorf("WS=%v，预期 [0]", tr.ws)
	}
	if tr.acc != 5 {
		t.Errorf("acc=%d，预期读到缓冲值 5", tr.acc)
	}
	if state[0] != 7 {
		t.Errorf("试执行修改了状态: %v", state)
	}
}

// 先读后写与先写后读的 RS 差异。
func TestReadWriteOrderRS(t *testing.T) {
	state := []int64{3}
	rw := execute(state, []Op{R(0), W(0, 1)})
	if !reflect.DeepEqual(rw.rs, []int{0}) {
		t.Errorf("先读后写 RS=%v，预期 [0]", rw.rs)
	}
	if rw.acc != 3 || rw.buf[0] != 4 {
		t.Errorf("先读后写 acc=%d buf=%v，预期 acc=3 buf{0:4}", rw.acc, rw.buf)
	}
	wr := execute(state, []Op{W(0, 1), R(0)})
	if len(wr.rs) != 0 {
		t.Errorf("先写后读 RS=%v，预期空", wr.rs)
	}
	if wr.acc != 1 || wr.buf[0] != 1 {
		t.Errorf("先写后读 acc=%d buf=%v，预期 acc=1 buf{0:1}", wr.acc, wr.buf)
	}
}

// 同键重复读 acc 累加两次，RS 仍只含该键一次。
func TestRepeatedReadAccumulates(t *testing.T) {
	state := []int64{5}
	tr := execute(state, []Op{R(0), R(0)})
	if tr.acc != 10 {
		t.Errorf("acc=%d，预期同键读两次累加为 10", tr.acc)
	}
	if !reflect.DeepEqual(tr.rs, []int{0}) {
		t.Errorf("RS=%v，预期 [0]", tr.rs)
	}
}

// RAW 与 WAR 同时出现才中止；仅其一仍提交。
func TestRAWAndWARRule(t *testing.T) {
	// 同时有 RAW 与 WAR → 中止回退。
	e := mustNew(t, 2, 2)
	mustSubmit(t, e, W(0, 1), R(1)) // T1: WS{0} RS{1}
	mustSubmit(t, e, R(0), W(1, 1)) // T2: RS{0} WS{1}，RAW(wres[0]=1) 且 WAR(rres[1]=1)
	res := e.RunBatch()
	checkBatch(t, res, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseFallback, 1, true}, // 重执行读到 T1 写入的 1
	})
	if got := stateOf(t, e, 2); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Errorf("终态=%v，预期 [1 2]", got)
	}

	// 仅 WAR → 提交。
	e2 := mustNew(t, 1, 2)
	mustSubmit(t, e2, R(0))    // T1: RS{0}
	mustSubmit(t, e2, W(0, 5)) // T2: WS{0}，WAR(rres[0]=1) 但无 RAW
	res2 := e2.RunBatch()
	checkBatch(t, res2, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseParallel, 0, true},
	})
	if got := stateOf(t, e2, 1); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("终态=%v，预期 [5]", got)
	}
}

// WAW 一律中止。
func TestWAWAlwaysAborts(t *testing.T) {
	e := mustNew(t, 1, 2)
	mustSubmit(t, e, W(0, 1)) // T1
	mustSubmit(t, e, W(0, 2)) // T2: WAW(wres[0]=1)
	res := e.RunBatch()
	checkBatch(t, res, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseFallback, 0, true},
	})
	if got := stateOf(t, e, 1); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("终态=%v，预期 [2]（回退者后写）", got)
	}
}

// 中止者的读写仍计入预留表，使他人被连带中止。
func TestAbortedTxStillReserves(t *testing.T) {
	e := mustNew(t, 2, 3)
	mustSubmit(t, e, W(0, 1))       // T1: WS{0}
	mustSubmit(t, e, R(1), W(0, 2)) // T2: RS{1} WS{0}，WAW 中止，但 RS 仍计入 rres[1]=2
	mustSubmit(t, e, R(0), W(1, 5)) // T3: RS{0} WS{1}，RAW(wres[0]=1) 且 WAR(rres[1]=2)
	res := e.RunBatch()
	// 若 T2 的读集未计入预留表，T3 无 WAR 本会提交。
	checkBatch(t, res, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseFallback, 0, true},
		{PhaseFallback, 2, true}, // 重执行读到 T2 写入的 2
	})
	if got := stateOf(t, e, 2); !reflect.DeepEqual(got, []int64{2, 7}) {
		t.Errorf("终态=%v，预期 [2 7]", got)
	}
}

// 回退阶段看到并行阶段提交者与更早回退者的写入。
func TestFallbackSeesCommittedWrites(t *testing.T) {
	e := mustNew(t, 1, 3)
	mustSubmit(t, e, W(0, 5))       // T1: 并行提交，写 0=5
	mustSubmit(t, e, R(0), W(0, 1)) // T2: WAW 回退，读到 5，写 0=6
	mustSubmit(t, e, R(0), W(0, 2)) // T3: WAW 回退，读到 T2 写的 6，写 0=8
	res := e.RunBatch()
	checkBatch(t, res, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseFallback, 5, true},
		{PhaseFallback, 6, true},
	})
	if got := stateOf(t, e, 1); !reflect.DeepEqual(got, []int64{8}) {
		t.Errorf("终态=%v，预期 [8]", got)
	}
}

// growKey0 通过反复倍增把键 0 的值放大到 7^rounds（每批一个事务：
// 7 次 R(0) 累加后 W(0,0) 写回 7 倍值），返回最终值。
func growKey0(t *testing.T, e *Executor, rounds int) int64 {
	t.Helper()
	mustSubmit(t, e, W(0, 1))
	e.RunBatch()
	v := int64(1)
	for i := 0; i < rounds; i++ {
		mustSubmit(t, e, R(0), R(0), R(0), R(0), R(0), R(0), R(0), W(0, 0))
		res := e.RunBatch()
		if len(res) != 1 || res[0].Phase != PhaseParallel {
			t.Fatalf("第 %d 轮倍增意外失败: %+v", i, res)
		}
		v *= 7
	}
	return v
}

// 溢出失败的事务不参与预留，也不回退。
func TestOverflowFailureNoReserveNoFallback(t *testing.T) {
	e := mustNew(t, 2, 2)
	v := growKey0(t, e, 22) // v = 7^22 ≈ 3.9e18，3v 溢出 int64
	if v*3 > 0 {
		t.Fatalf("前置假设错误：3v 未溢出，v=%d", v)
	}
	// T1 试执行时 3 次 R(0) 累加 3v 溢出 → 并行阶段失败。
	// 若 T1 的 RS/WS 被计入预留表，T2 将因 wres[0]=1 被 WAW/RAW 中止。
	mustSubmit(t, e, R(0), R(0), R(0), W(0, 0)) // T1: 溢出失败
	mustSubmit(t, e, R(0), W(0, 1))             // T2: 应并行提交
	res := e.RunBatch()
	checkBatch(t, res, 24, []wantRes{
		{PhaseFailed, 0, false},
		{PhaseParallel, v, true},
	})
	if got := stateOf(t, e, 2); !reflect.DeepEqual(got, []int64{v + 1, 0}) {
		t.Errorf("终态=%v，预期 [%d 0]（失败者不写、不回退）", got, v+1)
	}
	if e.Pending() != 0 {
		t.Errorf("失败者占用批容量且已处理，Pending=%d，预期 0", e.Pending())
	}
}

// 回退重执行时溢出同样失败，且不写入。
func TestFallbackOverflowFails(t *testing.T) {
	e := mustNew(t, 1, 2)
	v := growKey0(t, e, 22) // v = 7^22，2v 不溢出，4v 溢出
	if v*2 < 0 || v*4 > 0 {
		t.Fatalf("前置假设错误：v=%d", v)
	}
	mustSubmit(t, e, R(0), R(0), W(0, 0)) // T1: 试执行 acc=2v 不溢出，提交，写 0=2v
	mustSubmit(t, e, R(0), R(0), W(0, 0)) // T2: WAW 回退；重执行读到 2v，acc=4v 溢出
	res := e.RunBatch()
	checkBatch(t, res, 24, []wantRes{
		{PhaseParallel, 2 * v, true},
		{PhaseFailed, 0, false},
	})
	if got := stateOf(t, e, 1); !reflect.DeepEqual(got, []int64{2 * v}) {
		t.Errorf("终态=%v，预期 [%d]（回退溢出者不写入）", got, 2*v)
	}
}

// 溢出失败不是拒绝：占用批容量且事务号已消耗。
func TestOverflowConsumesTxIDAndCapacity(t *testing.T) {
	e := mustNew(t, 1, 2)
	growKey0(t, e, 22)
	id := mustSubmit(t, e, R(0), R(0), R(0), W(0, 0)) // 将溢出失败
	if id != 24 {
		t.Fatalf("事务号=%d，预期 24", id)
	}
	res := e.RunBatch()
	if len(res) != 1 || res[0].Phase != PhaseFailed {
		t.Fatalf("预期单个失败结局，得到 %+v", res)
	}
	next := mustSubmit(t, e, R(0))
	if next != 25 {
		t.Errorf("失败者的下一事务号=%d，预期 25（失败消耗事务号）", next)
	}
}

// 批容量截断与剩余顺延。
func TestBatchCapacityTruncation(t *testing.T) {
	e := mustNew(t, 2, 2)
	mustSubmit(t, e, W(0, 1)) // T1
	mustSubmit(t, e, W(1, 2)) // T2
	mustSubmit(t, e, W(0, 3)) // T3 顺延到下一批
	res1 := e.RunBatch()
	checkBatch(t, res1, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseParallel, 0, true},
	})
	if e.Pending() != 1 {
		t.Errorf("首批后 Pending=%d，预期 1", e.Pending())
	}
	res2 := e.RunBatch()
	checkBatch(t, res2, 3, []wantRes{{PhaseParallel, 0, true}})
	if e.Pending() != 0 {
		t.Errorf("次批后 Pending=%d，预期 0", e.Pending())
	}
	if got := stateOf(t, e, 2); !reflect.DeepEqual(got, []int64{3, 2}) {
		t.Errorf("终态=%v，预期 [3 2]", got)
	}
}

// 题目示例：普通规则（有 RAW 即中止）与本规则的差异。
// T2 有 RAW 但无 WAR，按本规则仍在并行阶段提交；
// 整体等价于串行次序 T2、T1、T3。
func TestSpecExample(t *testing.T) {
	e := mustNew(t, 3, 3)
	mustSubmit(t, e, W(0, 5))       // T1: RS{}  WS{0}
	mustSubmit(t, e, R(0), W(1, 1)) // T2: RS{0} WS{1}，仅 RAW，提交
	mustSubmit(t, e, R(1), W(0, 2)) // T3: RS{1} WS{0}，WAW，回退
	res := e.RunBatch()
	checkBatch(t, res, 1, []wantRes{
		{PhaseParallel, 0, true},
		{PhaseParallel, 0, true}, // 普通规则下会中止，本规则提交
		{PhaseFallback, 1, true},
	})
	if got := stateOf(t, e, 3); !reflect.DeepEqual(got, []int64{3, 1, 0}) {
		t.Errorf("终态=%v，预期 [3 1 0]", got)
	}
	// 与串行次序 T2、T1、T3 对照。
	serial := []int64{0, 0, 0}
	acc := execute(serial, []Op{R(0), W(1, 1)}) // T2
	serial[1] = acc.buf[1]
	acc = execute(serial, []Op{W(0, 5)}) // T1
	serial[0] = acc.buf[0]
	acc = execute(serial, []Op{R(1), W(0, 2)}) // T3
	serial[0] = acc.buf[0]
	if !reflect.DeepEqual(serial, []int64{3, 1, 0}) || acc.acc != 1 {
		t.Fatalf("串行对照自身有误: %v acc=%d", serial, acc.acc)
	}
	if got := stateOf(t, e, 3); !reflect.DeepEqual(got, serial) {
		t.Errorf("终态=%v 与串行次序 T2,T1,T3 的结果 %v 不一致", got, serial)
	}
}

// 相同提交序列与批边界得到完全相同的阶段、acc 与终态。
func TestDeterminism(t *testing.T) {
	run := func() ([]Result, []int64) {
		e := mustNew(t, 3, 2)
		var all []Result
		mustSubmit(t, e, W(0, 5))
		mustSubmit(t, e, R(0), W(1, 1))
		all = append(all, e.RunBatch()...)
		mustSubmit(t, e, R(1), W(0, 2))
		mustSubmit(t, e, R(0), R(1), W(2, 3))
		all = append(all, e.RunBatch()...)
		return all, stateOf(t, e, 3)
	}
	r1, s1 := run()
	r2, s2 := run()
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("两次运行结果不一致:\n%v\n%v", r1, r2)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Errorf("两次运行终态不一致: %v vs %v", s1, s2)
	}
}

// 预留表建立与判定触及的表项数不超过批内操作总数的两倍。
func TestReservationTouchBound(t *testing.T) {
	e := mustNew(t, 4, 4)
	ops := [][]Op{
		{W(0, 1), R(1)},
		{R(0), W(1, 1), R(2)},
		{W(2, 3)},
		{R(3), R(0), W(3, 1)},
	}
	total := 0
	for _, o := range ops {
		mustSubmit(t, e, o...)
		total += len(o)
	}
	e.RunBatch()
	if e.lastTouches > 2*total {
		t.Errorf("预留表触及 %d 项，超过批内操作总数 %d 的两倍", e.lastTouches, total)
	}
	t.Logf("预留表触及 %d 项，批内操作总数 %d", e.lastTouches, total)
}

// int64 溢出边界：acc 与缓冲值恰好为 MaxInt64/MinInt64 时不算溢出。
func TestOverflowBoundaryExact(t *testing.T) {
	tr := execute([]int64{math.MaxInt64}, []Op{R(0)})
	if tr.failed || tr.acc != math.MaxInt64 {
		t.Errorf("acc=MaxInt64 不应溢出: %+v", tr)
	}
	tr = execute([]int64{math.MaxInt64}, []Op{R(0), R(0)})
	if !tr.failed {
		t.Error("acc=2*MaxInt64 应溢出")
	}
	tr = execute([]int64{math.MinInt64}, []Op{R(0)})
	if tr.failed || tr.acc != math.MinInt64 {
		t.Errorf("acc=MinInt64 不应溢出: %+v", tr)
	}
	tr = execute([]int64{math.MaxInt64}, []Op{R(0), W(0, 1)})
	if !tr.failed {
		t.Error("缓冲值 MaxInt64+1 应溢出")
	}
}
