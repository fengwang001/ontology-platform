package slidingwindow

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
)

// scanMax 逐一扫描快照，作为最大值判定依据（朴素参照实现）。
func scanMax(values []float64) float64 {
	m := values[0]
	for _, v := range values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// requireMax 校验 Max 与逐元素扫描结果一致，并打印判定依据。
func requireMax(t *testing.T, w *Window, step string) {
	t.Helper()
	snap, snapMax, err := w.Snapshot()
	if err != nil {
		t.Fatalf("%s: snapshot error: %v", step, err)
	}
	got, err := w.Max()
	if err != nil {
		t.Fatalf("%s: max error: %v", step, err)
	}
	want := scanMax(snap)
	if got != want || snapMax != want {
		t.Fatalf("%s: max mismatch: Max()=%v SnapshotMax=%v scan=%v window=%v",
			step, got, snapMax, want, snap)
	}
	t.Logf("%-22s window=%v max=%v 判定依据=逐一扫描取最大", step, snap, got)
}

// TestTieMaxEvicted 覆盖并列最大值的维持与逐出。
func TestTieMaxEvicted(t *testing.T) {
	w, err := New(3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, v := range []float64{5, 3, 5} {
		ev, present, err := w.Append(v)
		if err != nil {
			t.Fatalf("append %v: %v", v, err)
		}
		t.Logf("输入=%v 自动逐出=%v 存在=%v 窗口=%v",
			v, ev, present, mustSnapshot(w))
	}
	requireMax(t, w, "并列最大值 [5 3 5]")

	ev, err := w.Evict()
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	t.Logf("显式逐出=%v 窗口=%v max 仍应为 5", ev, mustSnapshot(w))
	requireMax(t, w, "逐出第一个并列5")

	ev, err = w.Evict()
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	t.Logf("显式逐出=%v 窗口=%v", ev, mustSnapshot(w))
	got, _ := w.Max()
	if got != 5 {
		t.Fatalf("逐出3后剩余 [5]，期望 max=5，实际 %v", got)
	}
	t.Logf("显式逐出=%v max=%v 判定依据=剩余元素只有5", ev, got)

	ev, err = w.Evict()
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	t.Logf("显式逐出=%v 窗口已空", ev)
	if _, err := w.Max(); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("空窗口求最大值，期望 ErrEmptyWindow，实际 %v", err)
	}
	if _, err := w.Evict(); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("空窗口逐出，期望 ErrEmptyWindow，实际 %v", err)
	}
}

// TestMonotonicDecreasing 覆盖单调递减段：新元素更小，不产生尾部搬运，
// 队列保留全部元素；自动逐出走队首摘出路径（序号相等判定），不重扫整窗。
func TestMonotonicDecreasing(t *testing.T) {
	w, _ := New(4)
	for _, v := range []float64{10, 8, 6, 4} {
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("append %v: %v", v, err)
		}
		requireMax(t, w, fmt.Sprintf("递减段输入=%v", v))
	}
	if got := w.MoveCount(); got != 0 {
		t.Fatalf("递减段不应有尾部搬运，实际 moveCount=%d", got)
	}
	t.Logf("单调递减段 10>8>6>4：新值更小不挤出，moveCount=%d（队列保留全部元素）",
		w.MoveCount())

	ev, _, err := w.Append(2)
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	t.Logf("输入=2 自动逐出=%v 队列按序号摘队首，不重扫整窗", ev)
	if ev != 10 {
		t.Fatalf("期望自动逐出 10，实际 %v", ev)
	}
	requireMax(t, w, "自动逐出10后 [8 6 4 2]")
	if got := w.MoveCount(); got != 0 {
		t.Fatalf("递减全程不应有搬运，实际 moveCount=%d", got)
	}
}

// TestMonotonicIncreasing 单调递增段：每个旧队尾被新值搬运挤出，
// 搬运计数精确，队列内始终只有最新最大值。
func TestMonotonicIncreasing(t *testing.T) {
	w, _ := New(3)
	for _, v := range []float64{1, 2, 3} {
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("append %v: %v", v, err)
		}
		requireMax(t, w, fmt.Sprintf("递增段输入=%v", v))
	}
	if got := w.MoveCount(); got != 2 {
		t.Fatalf("递增段搬运计数期望 2（挤出1、挤出2），实际 %d", got)
	}
	t.Logf("单调递增段 1<2<3<4：每个旧队尾被搬运一次，moveCount=%d，每元素至多一次",
		w.MoveCount())
	ev, present, _ := w.Append(4)
	if !present || ev != 1 {
		t.Fatalf("期望自动逐出1，实际 ev=%v present=%v", ev, present)
	}
	requireMax(t, w, "递增自动逐出1后")
	if got := w.MoveCount(); got != 3 {
		t.Fatalf("搬运计数期望 3（再挤出3一次），实际 %d", got)
	}
}

// TestInvalidInputs 覆盖各类非法输入，并验证拒绝不改变内部结构与搬运计数。
func TestInvalidInputs(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		w, err := New(c)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("容量 %d：期望 ErrInvalidCapacity，实际 w=%v err=%v", c, w, err)
		}
		t.Logf("容量非法输入=%d 拒绝原因=ErrInvalidCapacity，窗口未创建", c)
	}

	w, _ := New(2)
	w.Append(7)
	snapshotBefore := mustSnapshot(w)
	movesBefore := w.MoveCount()

	if _, _, err := w.Append(math.NaN()); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("NaN：期望 ErrInvalidValue，实际 %v", err)
	}
	t.Logf("值非法输入=NaN 拒绝原因=ErrInvalidValue，结构与 moveCount 不变")
	assertUnchanged(t, w, snapshotBefore, movesBefore, "单值拒绝")

	evicted, err := w.AppendBatch([]float64{1, math.NaN()})
	if !errors.Is(err, ErrInvalidValue) || evicted != nil {
		t.Fatalf("含NaN批量：期望 ErrInvalidValue 且无逐出，实际 evicted=%v err=%v", evicted, err)
	}
	t.Logf("批量输入=[1 NaN] 任一值被拒整批不生效，拒绝原因=ErrInvalidValue")
	assertUnchanged(t, w, snapshotBefore, movesBefore, "批量拒绝")

	empty, err := w.AppendBatch(nil)
	if err != nil || empty != nil {
		t.Fatalf("空批量应为无操作成功，实际 evicted=%v err=%v", empty, err)
	}
	assertUnchanged(t, w, snapshotBefore, movesBefore, "空批量")

	if _, err := w.Evict(); err != nil {
		t.Fatalf("排空准备失败: %v", err)
	}
	if _, err := w.Evict(); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("空窗口逐出：期望 ErrEmptyWindow，实际 %v", err)
	}
	t.Logf("空窗口显式逐出 拒绝原因=ErrEmptyWindow")
	if _, err := w.Max(); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("空窗口求最大值：期望 ErrEmptyWindow，实际 %v", err)
	}
	t.Logf("空窗口求最大值 拒绝原因=ErrEmptyWindow")
}

func assertUnchanged(t *testing.T, w *Window, before []float64, moves int64, step string) {
	t.Helper()
	after := mustSnapshot(w)
	if len(after) != len(before) {
		t.Fatalf("%s：窗口长度被改变 before=%v after=%v", step, before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("%s：窗口内容被改变 before=%v after=%v", step, before, after)
		}
	}
	if w.MoveCount() != moves {
		t.Fatalf("%s：moveCount 被改变 before=%d after=%d", step, moves, w.MoveCount())
	}
}

func mustSnapshot(w *Window) []float64 {
	values, _, err := w.Snapshot()
	if err != nil {
		panic(err)
	}
	return values
}

// TestBatchAtomicOverflow 批量超出容量时按进入顺序自动逐出多余元素。
func TestBatchAtomicOverflow(t *testing.T) {
	w, _ := New(2)
	evicted, err := w.AppendBatch([]float64{9, 1, 8, 2})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(evicted) != 2 || evicted[0] != 9 || evicted[1] != 1 {
		t.Fatalf("期望逐出 [9 1]，实际 %v", evicted)
	}
	t.Logf("批量输入=[9 1 8 2] 容量=2 自动逐出=%v 保留=[8 2]", evicted)
	requireMax(t, w, "批量写满并溢出后")
}

// TestMoveCountBound 随机序列下搬运计数等于实际被挤出次数，
// 且永不超过累计进入元素数（每个元素至多被搬运一次）。
func TestMoveCountBound(t *testing.T) {
	w, _ := New(5)
	var pushed int64
	rng := rand.New(rand.NewSource(288))
	for i := 0; i < 500; i++ {
		v := float64(rng.Intn(20))
		if _, _, err := w.Append(v); err != nil {
			t.Fatalf("append: %v", err)
		}
		pushed++
		requireMax(t, w, fmt.Sprintf("随机序列 i=%d 输入=%v", i, v))
	}
	if w.MoveCount() > pushed {
		t.Fatalf("搬运计数 %d 超过进入元素数 %d", w.MoveCount(), pushed)
	}
	t.Logf("随机序列 500 次追加：moveCount=%d <= pushed=%d，每元素至多搬运一次",
		w.MoveCount(), pushed)
}

// TestConcurrentReadConsistency 并发读取下，Max 与同次快照逐一扫描一致。
func TestConcurrentReadConsistency(t *testing.T) {
	w, _ := New(64)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 20000; i++ {
			w.Append(float64(rng.Intn(1000)))
		}
	}()

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				snap, snapMax, err := w.Snapshot()
				if errors.Is(err, ErrEmptyWindow) {
					continue
				}
				if err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				if snapMax != scanMax(snap) {
					t.Errorf("并发读到不一致：max=%v scan=%v window=%v",
						snapMax, scanMax(snap), snap)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Log("8 个读协程 × 20000 次：Snapshot 内的最大值与同次窗口内容逐一扫描始终一致")
}

// TestDeterminism 同一输入序列反复计算，输出完全相同。
func TestDeterminism(t *testing.T) {
	sequence := []float64{4, 4, 9, 2, 9, 1, 8, 8, 3, 0, 12, 7, 6, 12, 5}
	run := func() struct {
		evicted []float64
		maxes   []float64
		moves   int64
	} {
		w, _ := New(4)
		res := struct {
			evicted []float64
			maxes   []float64
			moves   int64
		}{}
		for _, v := range sequence {
			ev, present, err := w.Append(v)
			if err != nil {
				t.Fatal(err)
			}
			if present {
				res.evicted = append(res.evicted, ev)
			}
			m, _ := w.Max()
			res.maxes = append(res.maxes, m)
		}
		res.moves = w.MoveCount()
		return res
	}

	first := run()
	for k := 0; k < 20; k++ {
		got := run()
		if got.moves != first.moves ||
			fmt.Sprint(got.evicted) != fmt.Sprint(first.evicted) ||
			fmt.Sprint(got.maxes) != fmt.Sprint(first.maxes) {
			t.Fatalf("第 %d 次运行输出不一致\nfirst=%+v\ngot  =%+v", k, first, got)
		}
	}
	t.Logf("同一序列重复21次输出完全相同：evicted=%v max序列=%v moveCount=%d",
		first.evicted, first.maxes, first.moves)
}
