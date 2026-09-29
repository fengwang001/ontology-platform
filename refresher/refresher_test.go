package refresher

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, debounce int64, maxPending int) *Refresher {
	t.Helper()
	r, err := New(debounce, maxPending)
	if err != nil {
		t.Fatalf("New(%d, %d) 失败: %v", debounce, maxPending, err)
	}
	return r
}

func mustUpsert(t *testing.T, r *Refresher, key, value string, at int64) {
	t.Helper()
	if err := r.Upsert(key, value, at); err != nil {
		t.Fatalf("Upsert(%q, %q, %d) 失败: %v", key, value, at, err)
	}
	t.Logf("输入: Upsert(key=%q, value=%q, at=%d) -> 结果: 已受理", key, value, at)
}

func mustAdvance(t *testing.T, r *Refresher, now int64) []Record {
	t.Helper()
	records, err := r.Advance(now)
	if err != nil {
		t.Fatalf("Advance(%d) 失败: %v", now, err)
	}
	t.Logf("输入: Advance(now=%d) -> 结果: 刷新记录=%v", now, records)
	return records
}

// 合并与顺延：同键连续变更合并为一个批次（条数加一、值更新、
// 刷新时刻顺延），且待刷新批次中的最新值在刷新前对外不可见。
func TestMergeAndPostpone(t *testing.T) {
	r := mustNew(t, 10, 8)

	mustUpsert(t, r, "a", "v1", 0)  // 新建批次，refreshAt=10
	mustUpsert(t, r, "a", "v2", 5)  // 并入，refreshAt 顺延为 15
	mustUpsert(t, r, "a", "v3", 12) // 并入，refreshAt 顺延为 22

	if _, ok := r.View("a"); ok {
		t.Fatalf("判定依据: 待刷新批次中的最新值对外不可见，但 View(\"a\") 已可见")
	}
	t.Logf("判定依据: 刷新前 View(\"a\") 不可见，符合“视图只在刷新时更新”")

	if got := mustAdvance(t, r, 21); len(got) != 0 {
		t.Fatalf("判定依据: refreshAt=22 > now=21 不应刷新，实际刷新 %v", got)
	}
	t.Logf("判定依据: now=21 < refreshAt=22，未刷新，顺延生效")

	records := mustAdvance(t, r, 22)
	want := []Record{{Key: "a", Value: "v3", Merged: 3, RefreshAt: 22, AppliedAt: 22}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("判定依据: 三次变更应合并为一条记录 %+v，实际 %+v", want, records)
	}
	t.Logf("判定依据: 三次变更合并为一条记录 %+v，条数=3、值取最新、刷新时刻取顺延值", records[0])

	if v, ok := r.View("a"); !ok || v != "v3" {
		t.Fatalf("判定依据: 刷新后视图应为 v3，实际 (%q, %v)", v, ok)
	}
	if snap := r.Snapshot(); snap.Refreshed != 1 || snap.Pending != 0 {
		t.Fatalf("判定依据: 累计刷新批数应为 1、待刷新应为 0，实际 %+v", snap)
	}
}

// 推进含等号边界：refreshAt == now 的批次必须被刷新。
func TestAdvanceInclusiveBoundary(t *testing.T) {
	r := mustNew(t, 10, 8)

	mustUpsert(t, r, "x", "1", 0) // refreshAt=10
	mustUpsert(t, r, "y", "1", 1) // refreshAt=11
	mustUpsert(t, r, "z", "1", 2) // refreshAt=12

	records := mustAdvance(t, r, 11)
	var gotKeys []string
	for _, rec := range records {
		gotKeys = append(gotKeys, rec.Key)
	}
	if !reflect.DeepEqual(gotKeys, []string{"x", "y"}) {
		t.Fatalf("判定依据: refreshAt<=11 的批次为 x(10)、y(11)，z(12) 不应刷新，实际 %v", gotKeys)
	}
	t.Logf("判定依据: refreshAt=11 == now=11 的 y 被刷新（等号边界），refreshAt=12 的 z 未刷新")

	if snap := r.Snapshot(); snap.Pending != 1 || snap.Refreshed != 2 {
		t.Fatalf("判定依据: 应剩 1 个待刷新、累计 2 批，实际 %+v", snap)
	}
}

// 停机收尾：全部待刷新批次立即刷新，与是否到达刷新时刻无关。
func TestShutdownDrainsPending(t *testing.T) {
	r := mustNew(t, 100, 8)

	mustUpsert(t, r, "a", "v1", 0)
	mustUpsert(t, r, "b", "v2", 1)
	mustUpsert(t, r, "b", "v3", 2)

	records := r.Shutdown()
	t.Logf("输入: Shutdown() -> 结果: 刷新记录=%v", records)

	want := []Record{
		{Key: "a", Value: "v1", Merged: 1, RefreshAt: 100, AppliedAt: 2},
		{Key: "b", Value: "v3", Merged: 2, RefreshAt: 102, AppliedAt: 2},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("判定依据: 停机应立即刷新全部待刷新批次 %+v，实际 %+v", want, records)
	}
	t.Logf("判定依据: 未到达刷新时刻(100/102)的批次在停机时被立即刷新，应用时刻=当前逻辑时钟 2")

	snap := r.Snapshot()
	if snap.Pending != 0 || snap.Refreshed != 2 {
		t.Fatalf("判定依据: 停机后待刷新应为 0、累计 2 批，实际 %+v", snap)
	}
	if v, _ := r.View("b"); v != "v3" {
		t.Fatalf("判定依据: 停机后视图应含最新值 v3，实际 %q", v)
	}

	if err := r.Upsert("c", "v", 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("判定依据: 停机后 Upsert 应返回 ErrClosed，实际 %v", err)
	}
	if again := r.Shutdown(); again != nil {
		t.Fatalf("判定依据: 重复停机应返回空记录，实际 %v", again)
	}
	t.Logf("判定依据: 停机后拒绝新变更(ErrClosed)，重复停机安全无副作用")
}

// 时钟单调：时间戳不得回退；并列时按到达顺序后者胜。
func TestClockMonotonic(t *testing.T) {
	r := mustNew(t, 10, 8)

	mustUpsert(t, r, "a", "v1", 5)

	err := r.Upsert("a", "bad", 4)
	if !errors.Is(err, ErrClockRegression) {
		t.Fatalf("判定依据: at=4 < 时钟=5 应返回 ErrClockRegression，实际 %v", err)
	}
	t.Logf("输入: Upsert(at=4) 当时钟=5 -> 结果: %v（时间回退被拒）", err)

	if _, advErr := r.Advance(4); !errors.Is(advErr, ErrClockRegression) {
		t.Fatalf("判定依据: Advance(4) < 时钟=5 应返回 ErrClockRegression，实际 %v", advErr)
	}
	t.Logf("输入: Advance(now=4) 当时钟=5 -> 结果: 时间回退被拒")

	if snap := r.Snapshot(); snap.Clock != 5 {
		t.Fatalf("判定依据: 被拒操作不得改变逻辑时钟，期望 5，实际 %d", snap.Clock)
	}

	// 并列时间戳：同一键 at 相等时按到达顺序后者胜。
	mustUpsert(t, r, "a", "v2", 5)
	mustUpsert(t, r, "a", "v3", 5)

	records := mustAdvance(t, r, 15)
	if len(records) != 1 || records[0].Value != "v3" || records[0].Merged != 3 {
		t.Fatalf("判定依据: 并列时间戳后者胜，应刷新 v3 且合并 3 条，实际 %+v", records)
	}
	t.Logf("判定依据: 三次 at=5 的变更按到达顺序合并，最终值=v3（后者胜），Merged=3")
}

// 四类非法输入：参数非正、键为空、时间回退、待刷新键数超限，
// 各自返回互不相同的可判定错误，且失败后状态不变、仍可正常使用。
func TestInvalidInputsRejected(t *testing.T) {
	// 1. 参数非正
	for _, args := range [][2]int64{{0, 1}, {-3, 1}, {1, 0}, {1, -2}} {
		if _, err := New(args[0], int(args[1])); !errors.Is(err, ErrNonPositiveParam) {
			t.Fatalf("判定依据: New(%d, %d) 应返回 ErrNonPositiveParam，实际 %v", args[0], args[1], err)
		}
		t.Logf("输入: New(debounce=%d, maxPending=%d) -> 结果: ErrNonPositiveParam", args[0], args[1])
	}

	r := mustNew(t, 10, 2)
	mustUpsert(t, r, "a", "v1", 5)
	before := r.Snapshot()
	t.Logf("基准状态: %+v", before)

	assertRejected := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("判定依据: %s 应返回 %v，实际 %v", name, want, err)
		}
		after := r.Snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("判定依据: %s 被拒后状态不得改变，前 %+v 后 %+v", name, before, after)
		}
		t.Logf("输入: %s -> 结果: %v；判定依据: 状态前后一致 %+v", name, err, after)
	}

	// 2. 键为空
	assertRejected("Upsert(key=\"\")", r.Upsert("", "v", 6), ErrEmptyKey)
	// 3. 时间回退
	assertRejected("Upsert(at=4)", r.Upsert("a", "v", 4), ErrClockRegression)
	assertRejected("Advance(now=4)", func() error { _, e := r.Advance(4); return e }(), ErrClockRegression)
	// 4. 待刷新键数超限（上限 2，已有 a，再建 b 后 c 超限）
	mustUpsert(t, r, "b", "v1", 6)
	before = r.Snapshot()
	assertRejected("Upsert(key=\"c\") 超限", r.Upsert("c", "v1", 7), ErrTooManyPending)

	// 四类错误互不相同，可用 errors.Is 区分。
	all := []error{ErrNonPositiveParam, ErrEmptyKey, ErrClockRegression, ErrTooManyPending}
	for i, e1 := range all {
		for j, e2 := range all {
			if i != j && errors.Is(e1, e2) {
				t.Fatalf("判定依据: %v 与 %v 必须互不相同", e1, e2)
			}
		}
	}
	t.Logf("判定依据: 四类错误 %v 互不相同且均可由 errors.Is 判定", all)

	// 被拒后仍可继续正常使用。
	mustUpsert(t, r, "a", "v2", 7) // 并入已有批次，不占新键位
	records := mustAdvance(t, r, 17)
	if len(records) != 2 {
		t.Fatalf("判定依据: 拒绝后实例仍可用，应刷新 2 批，实际 %+v", records)
	}
	if v, _ := r.View("a"); v != "v2" {
		t.Fatalf("判定依据: 拒绝后视图应正常更新为 v2，实际 %q", v)
	}
	t.Logf("判定依据: 四类非法输入被拒后实例仍可正常 Upsert/Advance/View")
}

// 并发只读一致：多 goroutine 并发读取同一实例，结果必须逐字段相同。
func TestConcurrentReadOnlyConsistency(t *testing.T) {
	r := mustNew(t, 10, 64)
	for i, key := range []string{"a", "b", "c"} {
		mustUpsert(t, r, key, "v1", int64(i))
	}
	mustAdvance(t, r, 20)
	mustUpsert(t, r, "a", "v2", 25) // 留一个待刷新批次

	want := r.Snapshot()
	t.Logf("基准快照: %+v", want)

	const readers = 32
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan string, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				snap := r.Snapshot()
				if !reflect.DeepEqual(snap, want) {
					errs <- "快照逐字段不一致"
					return
				}
				v, ok := r.View("a")
				if !ok || v != "v1" {
					errs <- "View 读取到未刷新值或缺失"
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatalf("判定依据: 并发只读结果必须逐字段相同，违规: %s", msg)
	}
	t.Logf("判定依据: %d 个读者 x %d 轮并发只读，快照与视图均逐字段相同", readers, rounds)
}
