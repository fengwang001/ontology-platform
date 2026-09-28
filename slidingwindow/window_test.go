package slidingwindow

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// scanMax 逐一扫描窗口内容求最大值，作为判定单调队列结果正确性的依据。
func scanMax(values []float64) float64 {
	m := values[0]
	for _, v := range values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// logState 打印输入、逐出值、窗口内容与最大值，并说明判定依据：
// 单调队列队首给出的最大值必须与逐一扫描窗口内容的结果一致。
func logState(t *testing.T, input string, evicted float64, didEvict bool, snap Snapshot) {
	t.Helper()
	basis := scanMax(snap.Values)
	evictStr := "无"
	if didEvict {
		evictStr = fmt.Sprintf("%v", evicted)
	}
	verdict := "单调队列队首 == 逐一扫描结果，正确"
	if snap.Max != basis {
		verdict = "不一致，错误"
	}
	t.Logf("%-14s 窗口=%v 逐出=%s 队首最大值=%v 扫描最大值=%v 判定依据=%s",
		input, snap.Values, evictStr, snap.Max, basis, verdict)
}

func mustSnapshot(t *testing.T, w *Window) Snapshot {
	t.Helper()
	snap, err := w.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 意外失败: %v", err)
	}
	return snap
}

// TestTieMaxEviction 覆盖并列最大值逐个被逐出的场景：
// 同值最大值离窗后，新的并列值必须继续维持正确最大值。
func TestTieMaxEviction(t *testing.T) {
	w, err := New(3)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	steps := []struct {
		input float64
		want  float64
	}{
		{5, 5},
		{5, 5},
		{3, 5},
		{1, 5}, // 自动逐出最早的 5，仍有并列的第二个 5
		{9, 9}, // 自动逐出第二个 5，新最大值 9
		{2, 9}, // 自动逐出 3
	}

	var totalEvicted float64
	for _, step := range steps {
		evicted, didEvict, perr := w.Push(step.input)
		if perr != nil {
			t.Fatalf("Push(%v) 意外失败: %v", step.input, perr)
		}
		if didEvict {
			totalEvicted += evicted
		}
		snap := mustSnapshot(t, w)
		logState(t, fmt.Sprintf("Push(%v)", step.input), evicted, didEvict, snap)
		if snap.Max != step.want {
			t.Fatalf("Push(%v) 后最大值 = %v, 期望 %v", step.input, snap.Max, step.want)
		}
		if got := scanMax(snap.Values); got != snap.Max {
			t.Fatalf("最大值 %v 与扫描结果 %v 不一致", snap.Max, got)
		}
	}

	// 显式逐出：剩余窗口 [1,9,2]，逐出 1 后最大值仍为 9。
	v, err := w.Evict()
	if err != nil || v != 1 {
		t.Fatalf("Evict = (%v,%v), 期望 (1,nil)", v, err)
	}
	snap := mustSnapshot(t, w)
	logState(t, "Evict()", v, true, snap)
	if snap.Max != 9 {
		t.Fatalf("逐出 1 后最大值 = %v, 期望 9", snap.Max)
	}

	// 再逐出 9，最大值必须回落到 2，证明被逐出的队首不会残留。
	v, err = w.Evict()
	if err != nil || v != 9 {
		t.Fatalf("Evict = (%v,%v), 期望 (9,nil)", v, err)
	}
	snap = mustSnapshot(t, w)
	logState(t, "Evict()", v, true, snap)
	if snap.Max != 2 {
		t.Fatalf("逐出 9 后最大值 = %v, 期望 2", snap.Max)
	}

	if totalEvicted != 5+5+3 {
		t.Fatalf("自动逐出值累计异常: %v", totalEvicted)
	}
}

// TestDecreasingRun 覆盖单调递减段：每个新元素都接在队尾且不弹出旧元素，
// 最大值始终为窗口内最早（最大）元素，离窗时队首直接出队。
func TestDecreasingRun(t *testing.T) {
	w, _ := New(4)
	inputs := []float64{8, 7, 6, 5, 4, 3, 2, 1}
	var pushed int64
	for _, v := range inputs {
		evicted, didEvict, perr := w.Push(v)
		if perr != nil {
			t.Fatalf("Push(%v) 失败: %v", v, perr)
		}
		pushed++
		snap := mustSnapshot(t, w)
		logState(t, fmt.Sprintf("Push(%v)", v), evicted, didEvict, snap)
		if snap.Max != snap.Values[0] {
			t.Fatalf("递减段最大值 %v 不是队首 %v", snap.Max, snap.Values[0])
		}
		if snap.Max != scanMax(snap.Values) {
			t.Fatalf("最大值与扫描结果不一致")
		}
	}
	// 递减段没有元素被队尾弹出，搬运次数恰好等于成功写入次数。
	if w.Moves() != pushed {
		t.Fatalf("递减段搬运次数 = %d, 期望 %d", w.Moves(), pushed)
	}
}

// TestInvalidInputs 覆盖容量非法、空窗口操作、值非法，
// 并断言被拒绝操作不改变窗口内容、长度与搬运计数。
func TestInvalidInputs(t *testing.T) {
	for _, cap := range []int{0, -1, -100} {
		if _, err := New(cap); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d) err = %v, 期望 ErrInvalidCapacity", cap, err)
		}
		t.Logf("New(%d) 被拒绝: %v（原因可区分）", cap, ErrInvalidCapacity)
	}

	w, _ := New(2)
	if _, err := w.Evict(); !errors.Is(err, ErrEmptyEvict) {
		t.Fatalf("空窗口 Evict err = %v, 期望 ErrEmptyEvict", err)
	}
	if _, err := w.Max(); !errors.Is(err, ErrEmptyMax) {
		t.Fatalf("空窗口 Max err = %v, 期望 ErrEmptyMax", err)
	}
	if _, err := w.Snapshot(); !errors.Is(err, ErrEmptyMax) {
		t.Fatalf("空窗口 Snapshot err = %v, 期望 ErrEmptyMax", err)
	}
	t.Logf("空窗口 Evict/Max/Snapshot 被拒绝: %v / %v", ErrEmptyEvict, ErrEmptyMax)

	// 先写入合法数据，用于校验非法操作的“无副作用”。
	if err := w.PushAll([]float64{3, 1}); err != nil {
		t.Fatalf("预置数据失败: %v", err)
	}
	before := mustSnapshot(t, w)
	movesBefore := w.Moves()

	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, _, err := w.Push(v); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("Push(%v) err = %v, 期望 ErrInvalidValue", v, err)
		}
		t.Logf("Push(%v) 被拒绝: %v（窗口与搬运计数不变）", v, ErrInvalidValue)
	}
	after := mustSnapshot(t, w)
	if fmt.Sprint(after.Values) != fmt.Sprint(before.Values) || after.Max != before.Max {
		t.Fatalf("非法 Push 改变了窗口: before=%v after=%v", before, after)
	}
	if w.Moves() != movesBefore || w.Len() != 2 {
		t.Fatalf("非法 Push 改变了搬运计数或长度")
	}

	// 空批次。
	if err := w.PushAll(nil); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("PushAll(nil) err = %v, 期望 ErrEmptyBatch", err)
	}

	// 批次中任一值非法则整批不生效（第三个值 -Inf）。
	if err := w.PushAll([]float64{9, 8, math.Inf(-1)}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("含非法值的批次应返回 ErrInvalidValue, 实际 %v", err)
	}
	unchanged := mustSnapshot(t, w)
	if fmt.Sprint(unchanged.Values) != fmt.Sprint(before.Values) {
		t.Fatalf("被拒绝批次产生了副作用: before=%v after=%v", before.Values, unchanged.Values)
	}
	if w.Moves() != movesBefore {
		t.Fatalf("被拒绝批次改变了搬运计数: %d -> %d", movesBefore, w.Moves())
	}
	t.Logf("含非法值批次整批回滚: 窗口仍为 %v, 搬运计数=%d", unchanged.Values, w.Moves())

	// 排空后再次确认空窗口错误，且窗口恢复后仍可正常使用。
	if _, err := w.Evict(); err != nil {
		t.Fatalf("Evict 失败: %v", err)
	}
	if _, err := w.Evict(); err != nil {
		t.Fatalf("Evict 失败: %v", err)
	}
	if _, err := w.Evict(); !errors.Is(err, ErrEmptyEvict) {
		t.Fatalf("排空后 Evict err = %v", err)
	}
	if _, err := w.Max(); !errors.Is(err, ErrEmptyMax) {
		t.Fatalf("排空后 Max err = %v", err)
	}
	if err := w.PushAll([]float64{42}); err != nil {
		t.Fatalf("排空后窗口应仍可使用: %v", err)
	}
	if max, _ := w.Max(); max != 42 {
		t.Fatalf("恢复后最大值 = %v, 期望 42", max)
	}
}

// TestSequenceConsistency 在混合输入序列的每一步比对队首最大值与
// 逐一扫描结果（自动逐出与显式逐出后都检查），并校验搬运次数
// 不超过成功写入次数（每个元素至多搬运一次）。
func TestSequenceConsistency(t *testing.T) {
	w, _ := New(5)
	inputs := []float64{2, 7, 3, 7, 8, 1, 8, 6, 0, -3, 9, 9, 2, 5, 5}
	var pushed int64
	for _, v := range inputs {
		evicted, didEvict, err := w.Push(v)
		if err != nil {
			t.Fatalf("Push(%v) 失败: %v", v, err)
		}
		pushed++
		snap := mustSnapshot(t, w)
		logState(t, fmt.Sprintf("Push(%v)", v), evicted, didEvict, snap)
		if snap.Max != scanMax(snap.Values) {
			t.Fatalf("Push(%v) 后队首最大值 %v 与扫描 %v 不一致", v, snap.Max, scanMax(snap.Values))
		}
		if w.Moves() > pushed {
			t.Fatalf("搬运次数 %d 超过写入次数 %d", w.Moves(), pushed)
		}
	}

	for w.Len() > 0 {
		v, err := w.Evict()
		if err != nil {
			t.Fatalf("Evict 失败: %v", err)
		}
		if w.Len() == 0 {
			if _, err := w.Max(); !errors.Is(err, ErrEmptyMax) {
				t.Fatalf("逐出至空后 Max err = %v, 期望 ErrEmptyMax", err)
			}
			t.Logf("Evict()        窗口=[] 逐出=%v 最大值不可用: %v（空窗判定正确）", v, ErrEmptyMax)
			break
		}
		snap := mustSnapshot(t, w)
		logState(t, "Evict()", v, true, snap)
		if snap.Max != scanMax(snap.Values) {
			t.Fatalf("Evict 后队首最大值与扫描不一致")
		}
	}
}

type opKind int

const (
	kindPush opKind = iota
	kindPushAll
	kindEvict
	kindMax
	kindSnapshot
)

type op struct {
	kind opKind
	val  float64
	vals []float64
}

// run 执行一串操作并记录全部输出，用于确定性比对。
func run(ops []op) []string {
	w, _ := New(3)
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		switch o.kind {
		case kindPush:
			evicted, didEvict, err := w.Push(o.val)
			out = append(out, fmt.Sprintf("push %v -> %v %v %v", o.val, evicted, didEvict, err))
		case kindPushAll:
			err := w.PushAll(o.vals)
			out = append(out, fmt.Sprintf("pushall %v -> %v", o.vals, err))
		case kindEvict:
			v, err := w.Evict()
			out = append(out, fmt.Sprintf("evict -> %v %v", v, err))
		case kindMax:
			v, err := w.Max()
			out = append(out, fmt.Sprintf("max -> %v %v", v, err))
		case kindSnapshot:
			snap, err := w.Snapshot()
			out = append(out, fmt.Sprintf("snapshot -> %v %v %v", snap.Values, snap.Max, err))
		}
	}
	return out
}

// TestDeterminism 同一输入序列（含合法与非法操作）反复计算，输出必须完全相同。
func TestDeterminism(t *testing.T) {
	ops := []op{
		{kind: kindMax},
		{kind: kindEvict},
		{kind: kindPush, val: 4},
		{kind: kindPush, val: 4},
		{kind: kindPush, val: 2},
		{kind: kindSnapshot},
		{kind: kindPush, val: math.NaN()},
		{kind: kindPush, val: 4},
		{kind: kindPushAll, vals: []float64{9, math.Inf(1)}},
		{kind: kindPushAll, vals: []float64{9}},
		{kind: kindSnapshot},
		{kind: kindEvict},
		{kind: kindMax},
	}

	first := run(ops)
	for iter := 0; iter < 5; iter++ {
		got := run(ops)
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("第 %d 次运行输出不一致:\nfirst=%v\ngot  =%v", iter, first, got)
		}
	}
	t.Logf("同一序列重复 6 次，输出完全一致: %v", first)
}

// TestConcurrentReads 写入端不断变更窗口（含满窗自动逐出），
// 多个读取端并发取一致性快照，每次读到的 Max 都必须与
// 同一份快照内容逐一扫描的结果相等。配合 -race 检测数据竞争。
func TestConcurrentReads(t *testing.T) {
	w, _ := New(7)
	stop := make(chan struct{})
	var writers sync.WaitGroup
	var readers sync.WaitGroup

	writers.Add(1)
	go func() {
		defer writers.Done()
		v := 0.0
		for {
			select {
			case <-stop:
				return
			default:
				v++
				if _, _, err := w.Push(v); err != nil {
					t.Errorf("并发 Push 失败: %v", err)
					return
				}
				if int(v)%5 == 0 {
					_, _ = w.Evict()
				}
			}
		}
	}()

	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 2000; i++ {
				snap, err := w.Snapshot()
				if err != nil {
					continue // 与空窗瞬间交错，允许
				}
				if got := scanMax(snap.Values); got != snap.Max {
					t.Errorf("并发快照不一致: max=%v scan=%v values=%v", snap.Max, got, snap.Values)
					return
				}
				if len(snap.Values) > w.Capacity() {
					t.Errorf("快照长度 %d 超过容量", len(snap.Values))
					return
				}
			}
		}()
	}

	// 读取协程完成固定轮次后停止写入并收尾。
	readers.Wait()
	close(stop)
	writers.Wait()

	snap := mustSnapshot(t, w)
	t.Logf("并发结束: len=%d moves=%d 快照=%v max=%v（判定依据：每次快照 Max==扫描值）",
		len(snap.Values), w.Moves(), snap.Values, snap.Max)
	if snap.Max != scanMax(snap.Values) {
		t.Fatalf("最终快照不一致")
	}
}
