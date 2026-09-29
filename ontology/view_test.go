package ontology

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// replayHistory 模拟后台重建：从空开始，把快照点之前的日志逐条重放进重建缓冲区。
func replayHistory(t *testing.T, v *View) {
	t.Helper()
	cut := v.SnapshotPoint()
	history := v.LogEvents()[:cut]
	t.Logf("重放输入: snapshotSeq=%d, 历史事件数=%d, 事件=%v", cut, len(history), history)
	for _, e := range history {
		if err := v.Replay([]Event{e}); err != nil {
			t.Fatalf("重放失败: event=%v err=%v", e, err)
		}
	}
}

func expectMap(t *testing.T, got map[string]int64, want map[string]int64, reason string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 视图不一致 got=%v want=%v（判定依据: 键数量不同）", reason, got, want)
	}
	for key, wantCount := range want {
		if got[key] != wantCount {
			t.Fatalf("%s: 视图不一致 got=%v want=%v（判定依据: 键 %q 计数 %d != %d）",
				reason, got, want, key, got[key], wantCount)
		}
	}
	t.Logf("视图校验通过 [%s]: %v", reason, got)
}

func naiveReplay(events []Event) map[string]int64 {
	out := make(map[string]int64)
	for _, e := range events {
		out[e.Key] += e.Delta
	}
	return out
}

// TestReplayMatchesNaive 验证：重建重放得到的视图与朴素重放全量日志一致。
func TestReplayMatchesNaive(t *testing.T) {
	v := New()
	seed := []Event{{"a", 1}, {"b", 2}, {"a", 4}, {"b", -1}, {"c", 10}}
	if err := v.Apply(seed); err != nil {
		t.Fatalf("Apply 返回错误: %v", err)
	}
	t.Logf("输入: 首批事件=%v; 结果: 前台=%v; 判定依据: 朴素累加 a=1+4, b=2-1, c=10",
		seed, v.Snapshot())
	expectMap(t, v.Snapshot(), map[string]int64{"a": 5, "b": 1, "c": 10}, "重建前前台")

	seq, err := v.BeginRebuild()
	if err != nil {
		t.Fatalf("BeginRebuild 返回错误: %v", err)
	}
	t.Logf("输入: BeginRebuild; 结果: snapshotSeq=%d rebuilding=%v; 判定依据: 等于日志长度 %d",
		seq, v.Rebuilding(), len(v.LogEvents()))
	if seq != int64(len(seed)) {
		t.Fatalf("快照点错误: got=%d want=%d", seq, len(seed))
	}

	replayHistory(t, v)
	expectMap(t, v.Snapshot(), map[string]int64{"a": 5, "b": 1, "c": 10}, "重放期间仍读前台")

	if err := v.SwitchRebuild(); err != nil {
		t.Fatalf("SwitchRebuild 返回错误: %v", err)
	}
	t.Logf("输入: SwitchRebuild; 结果: rebuilding=%v 前台=%v", v.Rebuilding(), v.Snapshot())
	expectMap(t, v.Snapshot(), naiveReplay(seed), "切换后与朴素重放一致")
}

// TestDoubleWriteAndCatchup 验证：双写 + 切换前补齐，重建结果包含重建期间的全部新事件。
func TestDoubleWriteAndCatchup(t *testing.T) {
	v := New()
	history := []Event{{"a", 1}, {"a", 2}}
	if err := v.Apply(history); err != nil {
		t.Fatal(err)
	}
	seq, _ := v.BeginRebuild()
	t.Logf("重建开始: snapshotSeq=%d 前台=%v", seq, v.Snapshot())

	during1 := []Event{{"a", 10}, {"b", 7}}
	during2 := []Event{{"b", -2}}
	if err := v.Apply(during1); err != nil {
		t.Fatal(err)
	}
	t.Logf("双写输入(1): %v; 结果: 前台=%v rebuilding=%v; 判定依据: 重建期间新事件立即进前台并记入待补齐",
		during1, v.Snapshot(), v.Rebuilding())
	expectMap(t, v.Snapshot(), map[string]int64{"a": 13, "b": 7}, "重建期间前台即时更新")

	replayHistory(t, v) // 后台只重放快照点之前的历史。

	if err := v.Apply(during2); err != nil {
		t.Fatal(err)
	}
	t.Logf("双写输入(2): %v; 结果: 前台=%v", during2, v.Snapshot())

	if err := v.SwitchRebuild(); err != nil {
		t.Fatal(err)
	}
	all := append(append([]Event{}, history...), append(during1, during2...)...)
	t.Logf("切换完成: rebuilding=%v 前台=%v; 判定依据: 必须等于全量日志朴素重放 %v",
		v.Rebuilding(), v.Snapshot(), naiveReplay(all))
	expectMap(t, v.Snapshot(), naiveReplay(all), "补齐后切换与全量朴素重放一致")
}

// TestAbortRebuild 验证：中止丢弃缓冲区与待补齐列表，前台不变，且可重新开始重建。
func TestAbortRebuild(t *testing.T) {
	v := New()
	if err := v.Apply([]Event{{"a", 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.BeginRebuild(); err != nil {
		t.Fatal(err)
	}
	if err := v.Replay([]Event{{"a", 1}}); err != nil {
		t.Fatal(err)
	}
	if err := v.Apply([]Event{{"b", 5}}); err != nil {
		t.Fatal(err)
	}
	before := v.Snapshot()

	if err := v.AbortRebuild(); err != nil {
		t.Fatalf("AbortRebuild 返回错误: %v", err)
	}
	t.Logf("输入: AbortRebuild; 结果: rebuilding=%v 前台=%v; 判定依据: 前台与中止前 %v 完全相同",
		v.Rebuilding(), v.Snapshot(), before)
	expectMap(t, v.Snapshot(), before, "中止后前台不变")

	if _, err := v.BeginRebuild(); err != nil {
		t.Fatalf("中止后重新开始重建失败: %v", err)
	}
	replayHistory(t, v)
	if err := v.SwitchRebuild(); err != nil {
		t.Fatal(err)
	}
	expectMap(t, v.Snapshot(), map[string]int64{"a": 1, "b": 5}, "重新重建后仍包含全部日志")
}

// TestControlErrors 验证四类非法控制操作返回互不相同的错误。
func TestControlErrors(t *testing.T) {
	v := New()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"非重建中切换", v.SwitchRebuild, ErrSwitchWithoutRebuild},
		{"非重建中中止", v.AbortRebuild, ErrAbortWithoutRebuild},
		{"非重建中重放", func() error { return v.Replay([]Event{{"a", 1}}) }, ErrReplayWithoutRebuild},
	}
	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got=%v want=%v", tc.name, err, tc.want)
		}
		t.Logf("输入: %s; 结果: err=%v; 判定依据: errors.Is 命中对应哨兵错误", tc.name, err)
	}

	if _, err := v.BeginRebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.BeginRebuild(); !errors.Is(err, ErrRebuildAlreadyActive) {
		t.Fatalf("重建中重复开始: got=%v want=%v", err, ErrRebuildAlreadyActive)
	}
	t.Logf("输入: 重建中再次 BeginRebuild; 结果: err=%v; 判定依据: errors.Is 命中 ErrRebuildAlreadyActive",
		ErrRebuildAlreadyActive)

	all := []error{
		ErrSwitchWithoutRebuild, ErrAbortWithoutRebuild,
		ErrRebuildAlreadyActive, ErrReplayWithoutRebuild,
		ErrEmptyKey, ErrZeroDelta,
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Fatalf("错误不满足互异: %v 与 %v", all[i], all[j])
			}
		}
	}
	t.Logf("输入: 六类错误两两比较; 结果: 互不相同; 判定依据: 两两 errors.Is 均为 false")
}

// TestInvalidEventsRejected 验证：空键 / 零增量被拒，且整批不生效、任何状态不变。
func TestInvalidEventsRejected(t *testing.T) {
	cases := []struct {
		name   string
		replay bool
		batch  []Event
		want   error
	}{
		{"空键(Apply)", false, []Event{{"a", 1}, {"", 2}, {"b", 3}}, ErrEmptyKey},
		{"零增量(Apply)", false, []Event{{"a", 1}, {"b", 0}}, ErrZeroDelta},
		{"空键(Replay)", true, []Event{{"", 1}}, ErrEmptyKey},
		{"零增量(Replay)", true, []Event{{"a", 0}}, ErrZeroDelta},
	}

	for _, tc := range cases {
		v := New()
		if err := v.Apply([]Event{{"x", 9}}); err != nil {
			t.Fatal(err)
		}
		if _, err := v.BeginRebuild(); err != nil {
			t.Fatal(err)
		}
		if err := v.Replay([]Event{{"x", 9}}); err != nil {
			t.Fatal(err)
		}
		beforeFront := v.Snapshot()
		beforeLog := len(v.LogEvents())

		var err error
		if tc.replay {
			err = v.Replay(tc.batch)
		} else {
			err = v.Apply(tc.batch)
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got=%v want=%v", tc.name, err, tc.want)
		}
		var batchErr *BatchError
		if !errors.As(err, &batchErr) {
			t.Fatalf("%s: 错误未包装 BatchError", tc.name)
		}
		t.Logf("输入(%s): %v; 结果: err=%v 非法位置=%d; 判定依据: errors.Is 命中 %v，整批回滚",
			tc.name, tc.batch, err, batchErr.Index, tc.want)
		expectMap(t, v.Snapshot(), beforeFront, tc.name+" 前台不变")
		if len(v.LogEvents()) != beforeLog {
			t.Fatalf("%s: 日志长度被改变 %d != %d", tc.name, len(v.LogEvents()), beforeLog)
		}
		// 若被拒批次曾污染重建缓冲区，切换后前台会变化——这里用“切换后仍等于旧前台”佐证。
		if err := v.SwitchRebuild(); err != nil {
			t.Fatal(err)
		}
		expectMap(t, v.Snapshot(), beforeFront, tc.name+" 重建缓冲区也未被污染")
	}
}

// TestConcurrentWritersAndReaders 验证并发写入与并发读下最终计数正确，
// 且任一时刻读到的快照都是自洽完整的。
func TestConcurrentWritersAndReaders(t *testing.T) {
	v := New()
	const writers = 8
	const perWriter = 200

	var applied int64 // 已完成的 Apply 次数
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				done := atomic.LoadInt64(&applied)
				snap := v.Snapshot()
				var sum int64
				for _, count := range snap {
					sum += count
				}
				// 先读完成计数再取快照：全部增量为 +1，计数总和必然在 [取数时已完成数, 提交总数] 区间内，
				// 且快照在读锁内整体拷贝，遍历时不会与写者竞争。
				if sum < done || sum > int64(writers*perWriter) {
					t.Errorf("读到不自洽快照: sum=%d applied=%d", sum, done)
					return
				}
			}
		}
	}()

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(id int) {
			defer writerWG.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("k%d", id%4)
				if err := v.Apply([]Event{{key, 1}}); err != nil {
					t.Errorf("并发 Apply 失败: %v", err)
					return
				}
				atomic.AddInt64(&applied, 1)
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	total := int64(writers * perWriter)
	snap := v.Snapshot()
	var got int64
	for _, count := range snap {
		got += count
	}
	t.Logf("输入: %d 个写者各 %d 条 +1 事件（共 %d）; 结果: 快照=%v 计数总和=%d; 判定依据: 总和必须等于 %d",
		writers, perWriter, total, snap, got, total)
	if got != total {
		t.Fatalf("最终计数错误: got=%d want=%d", got, total)
	}
}

// TestConcurrentRebuildSwitch 验证并发写入期间执行重建与切换，结果与朴素重放全量日志一致。
func TestConcurrentRebuildSwitch(t *testing.T) {
	v := New()

	for i := 0; i < 500; i++ {
		if err := v.Apply([]Event{{fmt.Sprintf("h%d", i%20), 2}}); err != nil {
			t.Fatal(err)
		}
	}

	seq, err := v.BeginRebuild()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("重建开始 snapshotSeq=%d，前台重建期间继续服务", seq)

	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for i := 0; i < 500; i++ {
			if err := v.Apply([]Event{{fmt.Sprintf("n%d", i%10), 1}}); err != nil {
				t.Errorf("重建期间 Apply 失败: %v", err)
				return
			}
		}
	}()

	// 后台逐条重放快照点之前的历史；重建期间的新事件不交给 Replay，只靠切换前补齐。
	history := v.LogEvents()[:seq]
	for _, e := range history {
		if err := v.Replay([]Event{e}); err != nil {
			t.Fatalf("并发重建重放失败: %v", err)
		}
	}
	writerWG.Wait()

	if err := v.SwitchRebuild(); err != nil {
		t.Fatal(err)
	}
	final := v.Snapshot()
	want := naiveReplay(v.LogEvents())
	t.Logf("输入: 500 条历史 + 重建并发 500 条新事件后切换; 结果: 前台键数=%d; 判定依据: 与全量日志朴素重放逐键比较",
		len(final))
	expectMap(t, final, want, "并发重建切换后与朴素重放一致")
}
