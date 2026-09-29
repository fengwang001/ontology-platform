package median

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveRef 是排序后朴素取值的参照实现。
type naiveRef struct {
	vals []int
}

func (r *naiveRef) add(v int) { r.vals = append(r.vals, v) }

func (r *naiveRef) withdraw(v int) bool {
	for i, x := range r.vals {
		if x == v {
			r.vals = append(r.vals[:i], r.vals[i+1:]...)
			return true
		}
	}
	return false
}

func (r *naiveRef) median() (int, bool) {
	if len(r.vals) == 0 {
		return 0, false
	}
	sorted := append([]int(nil), r.vals...)
	sort.Ints(sorted)
	return sorted[(len(sorted)-1)/2], true // 偶数取下中位数
}

func (r *naiveRef) len() int { return len(r.vals) }

// stepLogger 逐步打印输入、中位数与判定依据。
type stepLogger struct {
	t      *testing.T
	sb     strings.Builder
	stepNo int
}

func (l *stepLogger) log(format string, args ...any) {
	l.stepNo++
	line := fmt.Sprintf("step %d: %s", l.stepNo, fmt.Sprintf(format, args...))
	l.t.Log(line)
	l.sb.WriteString(line + "\n")
}

func verify(t *testing.T, tr *Tracker, ref *naiveRef, lg *stepLogger, stage string) {
	t.Helper()
	gotLen := tr.Len()
	if gotLen != ref.len() {
		t.Fatalf("%s: len mismatch tracker=%d naive=%d", stage, gotLen, ref.len())
	}
	want, ok := ref.median()
	got, err := tr.Median()
	if !ok {
		if !errors.Is(err, ErrEmpty) {
			t.Fatalf("%s: empty set median must return ErrEmpty, got %v", stage, err)
		}
		lg.log("[%s] len=%d median=ErrEmpty 依据=参照多重集为空", stage, gotLen)
	} else {
		if err != nil {
			t.Fatalf("%s: unexpected median error %v", stage, err)
		}
		if got != want {
			t.Fatalf("%s: median mismatch tracker=%d naive=%d", stage, got, want)
		}
		lg.log("[%s] len=%d median=%d 依据=排序后位置(n-1)/2=%d 的朴素取值=%d",
			stage, gotLen, got, (gotLen-1)/2, want)
	}
	if msg, cerr := tr.Check(); cerr != nil || msg != "" {
		t.Fatalf("%s: self-check failed: %q err=%v", stage, msg, cerr)
	}
}

func TestLowerMedianEvenOdd(t *testing.T) {
	tr := NewTracker(0)
	ref := &naiveRef{}
	lg := &stepLogger{t: t}

	seq := []int{3, 1, 4, 2} // 依次形成 n=1..4
	for _, v := range seq {
		if err := tr.Add(v); err != nil {
			t.Fatalf("add %d: %v", v, err)
		}
		ref.add(v)
		lg.log("输入 Add(%d)", v)
		verify(t, tr, ref, lg, "add")
	}
	// n=4 排序 [1 2 3 4]，下中位数为 2
	if m, _ := tr.Median(); m != 2 {
		t.Fatalf("even lower median want 2 got %d", m)
	}
	// 撤回后 n=3 排序 [1 3 4]，中位数 3；n=2 排序 [1 4]，下中位数 1
	for _, v := range []int{2, 3} {
		if err := tr.Withdraw(v); err != nil {
			t.Fatalf("withdraw %d: %v", v, err)
		}
		if !ref.withdraw(v) {
			t.Fatalf("naive ref missing %d", v)
		}
		lg.log("输入 Withdraw(%d)", v)
		verify(t, tr, ref, lg, "withdraw")
	}
}

func TestDuplicatesWithdrawOne(t *testing.T) {
	tr := NewTracker(0)
	ref := &naiveRef{}
	lg := &stepLogger{t: t}

	for _, v := range []int{5, 5, 5, 1, 9} {
		if err := tr.Add(v); err != nil {
			t.Fatalf("add: %v", err)
		}
		ref.add(v)
		lg.log("输入 Add(%d)", v)
	}
	verify(t, tr, ref, lg, "dups-loaded")

	// 只撤回一个 5：仍应剩两个 5，中位数保持正确。
	if err := tr.Withdraw(5); err != nil {
		t.Fatalf("withdraw one duplicate: %v", err)
	}
	ref.withdraw(5)
	lg.log("输入 Withdraw(5) 仅撤回一个重复副本")
	verify(t, tr, ref, lg, "dups-one-withdrawn")

	// 再撤回两个 5 后，5 不再存在。
	for i := 0; i < 2; i++ {
		if err := tr.Withdraw(5); err != nil {
			t.Fatalf("withdraw %d-th 5: %v", i, err)
		}
		ref.withdraw(5)
		lg.log("输入 Withdraw(5) 第 %d 次", i+2)
	}
	verify(t, tr, ref, lg, "dups-gone")

	err := tr.Withdraw(5)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdraw absent duplicate want ErrNotFound, got %v", err)
	}
	oe, ok := asOpError(err)
	if !ok || oe.Index != 0 {
		t.Fatalf("expected OpError index 0, got %#v", oe)
	}
	lg.log("输入 Withdraw(5) 被拒绝: %v 依据=该值当前有效副本数为 0", err)
	verify(t, tr, ref, lg, "after-notfound-reject")
}

func TestStaleCopiesAreCleaned(t *testing.T) {
	tr := NewTracker(0)
	for _, v := range []int{10, 20, 30, 40, 50} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	// 反复撤回当前较小侧堆顶：作废副本应在当次 prune 中立刻被清理出堆，账本清零。
	for _, v := range []int{30, 20, 40} {
		if err := tr.Withdraw(v); err != nil {
			t.Fatalf("withdraw %d: %v", v, err)
		}
		tr.small.prune()
		tr.large.prune()
		if len(tr.small.pending) != 0 {
			t.Fatalf("stale copies pending in small: %v", tr.small.pending)
		}
		if len(tr.large.pending) != 0 {
			t.Fatalf("stale copies pending in large: %v", tr.large.pending)
		}
	}
	if m, err := tr.Median(); err != nil || m != 10 {
		t.Fatalf("after cleanup median want 10 got %d err=%v", m, err)
	}
	if msg, err := tr.Check(); err != nil || msg != "" {
		t.Fatalf("check failed: %q %v", msg, err)
	}

	// 撤回堆深处的值时允许其作废副本暂时等待；它浮到堆顶后必须被清掉，
	// 且全程中位数与朴素取值一致。
	if err := tr.Withdraw(10); err != nil {
		t.Fatalf("withdraw deep 10: %v", err) // 剩 {50}
	}
	if m, err := tr.Median(); err != nil || m != 50 {
		t.Fatalf("median want 50 got %d err=%v", m, err)
	}
	if len(tr.small.pending) != 0 || len(tr.large.pending) != 0 {
		t.Fatalf("deep stale copy not drained after surfacing: small=%v large=%v",
			tr.small.pending, tr.large.pending)
	}
	t.Log("输入: 加入 [10..50]，撤回堆顶 30,20,40（当次清理），再撤回深处 10（浮顶后清理）; median=50; 依据=作废副本仅在堆顶 prune 弹出, 最终 pending 账本为空")
}

func TestErrorsAndNoTraceLeft(t *testing.T) {
	tr := NewTracker(3)
	ref := &naiveRef{}
	lg := &stepLogger{t: t}

	snapshot := func() string {
		return fmt.Sprintf("count=%d smallValid=%d largeValid=%d smallLen=%d largeLen=%d freq=%v",
			tr.count, tr.small.valid, tr.large.valid, len(tr.small.data), len(tr.large.data), tr.freq)
	}

	for _, v := range []int{1, 2} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
		ref.add(v)
	}
	before := snapshot()

	// 空批次 -> ErrInvalidArgument
	if err := tr.Commit(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty batch want ErrInvalidArgument got %v", err)
	}
	lg.log("输入 Commit(nil) 拒绝原因=%v 依据=批次为空", ErrInvalidArgument)

	// 未知操作类型 -> ErrInvalidArgument，且带有操作下标
	err := tr.Commit([]Op{{Kind: OpKind(99), Value: 1}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad kind want ErrInvalidArgument got %v", err)
	}
	if oe, ok := asOpError(err); !ok || oe.Index != 0 {
		t.Fatalf("bad kind OpError index wrong: %v", err)
	}
	lg.log("输入 Commit([{Kind:99}]) 拒绝原因=%v 依据=未知操作类型", err)

	// 撤回不存在 -> ErrNotFound（即使作废副本还堆在结构里也不允许）
	err = tr.Commit([]Op{{Kind: OpWithdraw, Value: 7}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdraw missing want ErrNotFound got %v", err)
	}
	oe, _ := asOpError(err)
	if oe.Index != 0 || oe.Op.Value != 7 {
		t.Fatalf("notfound OpError wrong: %+v", oe)
	}
	lg.log("输入 Commit(Withdraw 7) 拒绝原因=%v 依据=7 的有效副本数为 0", err)

	// 超限 -> ErrLimitExceeded（上限 3，当前 2，加入 2 个会到 4）
	err = tr.Commit([]Op{{Kind: OpAdd, Value: 3}, {Kind: OpAdd, Value: 4}})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("over limit want ErrLimitExceeded got %v", err)
	}
	lg.log("输入 Commit(Add 3, Add 4) 拒绝原因=%v 依据=提交后个数 4 超过上限 3", err)

	// 混合批次：先合法加入后撤回不存在值 -> 整批拒绝，状态不变
	err = tr.Commit([]Op{
		{Kind: OpAdd, Value: 8},
		{Kind: OpWithdraw, Value: 99},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("mixed batch want ErrNotFound got %v", err)
	}
	if oe, ok := asOpError(err); !ok || oe.Index != 1 {
		t.Fatalf("mixed batch index want 1, got %v", err)
	}
	lg.log("输入 Commit(Add 8, Withdraw 99) 拒绝原因=%v 依据=op[1] 撤回不存在值, 整批回滚", err)

	// 同批内撤回次数超过加入结果也必须拒绝
	err = tr.Commit([]Op{
		{Kind: OpAdd, Value: 1},
		{Kind: OpWithdraw, Value: 1},
		{Kind: OpWithdraw, Value: 1},
		{Kind: OpWithdraw, Value: 1},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("batch overdraft want ErrNotFound got %v", err)
	}
	if oe, ok := asOpError(err); !ok || oe.Index != 3 {
		t.Fatalf("batch overdraft index want 3, got %v", err)
	}
	lg.log("输入 Commit(Add 1, Withdraw 1 x3) 拒绝原因=%v 依据=op[3] 使 1 的净副本数为负", err)

	if after := snapshot(); after != before {
		t.Fatalf("rejected batch left traces:\nbefore=%s\nafter =%s", before, after)
	}
	verify(t, tr, ref, lg, "rejected-batches")

	// 空集查询报错
	empty := NewTracker(0)
	if _, err := empty.Median(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty median want ErrEmpty got %v", err)
	}
	lg.log("输入 Median() 拒绝原因=%v 依据=多重集为空", ErrEmpty)
}

func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))
	tr := NewTracker(0)
	ref := &naiveRef{}
	lg := &stepLogger{t: t}

	// 从一个有限值域抽取，制造重复值与撤回作废副本。
	for i := 0; i < 2000; i++ {
		v := rng.Intn(8)
		if rng.Intn(2) == 0 || ref.len() == 0 {
			if err := tr.Add(v); err != nil {
				t.Fatalf("iter %d add %d: %v", i, v, err)
			}
			ref.add(v)
			lg.log("输入 Add(%d)", v)
		} else {
			err := tr.Withdraw(v)
			present := ref.len() > 0 && contains(ref.vals, v)
			if present {
				if err != nil {
					t.Fatalf("iter %d withdraw %d: %v", i, v, err)
				}
				ref.withdraw(v)
				lg.log("输入 Withdraw(%d)", v)
			} else {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("iter %d withdraw absent %d want ErrNotFound got %v", i, v, err)
				}
				lg.log("输入 Withdraw(%d) 拒绝: %v", v, err)
			}
		}
		if i%50 == 0 {
			verify(t, tr, ref, lg, fmt.Sprintf("iter %d", i))
		}
	}
	verify(t, tr, ref, lg, "final")
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
