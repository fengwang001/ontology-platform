package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// naiveReplay 朴素重放：从空开始按顺序累加全部事件，作为一致性判定基准。
func naiveReplay(events []Event) map[string]int64 {
	out := make(map[string]int64)
	for _, ev := range events {
		out[ev.Key] += ev.Delta
	}
	return out
}

// replayAll 逐条重放快照内的全部历史事件。
func replayAll(t *testing.T, v *View) {
	t.Helper()
	for {
		ev, done, err := v.ReplayNext()
		if err != nil {
			t.Fatalf("ReplayNext 返回错误: %v", err)
		}
		if done {
			return
		}
		t.Logf("重放事件 %+v 到后台缓冲", ev)
	}
}

// TestRebuildReplayMatchesNaive 覆盖重建重放：后台缓冲从空按日志重放，
// 切换后前台必须与朴素重放结果一致。
func TestRebuildReplayMatchesNaive(t *testing.T) {
	v := NewView()
	input := []Event{{"a", 1}, {"b", 2}, {"a", 3}, {"c", 5}, {"b", -1}}
	t.Logf("输入事件: %+v", input)
	if err := v.Apply(input...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	replayAll(t, v)
	if err := v.Switch(); err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	got := v.Snapshot()
	want := naiveReplay(input)
	t.Logf("切换后前台: %v, 朴素重放基准: %v", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("重建重放结果与朴素重放不一致: got=%v want=%v", got, want)
	}
	t.Log("判定依据: 双缓冲重建重放结果 == 朴素重放结果")
}

// TestDoubleWriteDuringRebuild 覆盖双写：重建期间到达的新事件
// 既要立即应用到前台，也要记入待补齐列表。
func TestDoubleWriteDuringRebuild(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}, {"b", 2}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	during := []Event{{"a", 10}, {"c", 7}}
	t.Logf("重建期间到达事件: %+v", during)
	if err := v.Apply(during...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	gotA, _ := v.Get("a")
	gotC, _ := v.Get("c")
	t.Logf("重建期间前台读: a=%d c=%d", gotA, gotC)
	if gotA != 11 || gotC != 7 {
		t.Fatalf("新事件未立即应用到前台: a=%d c=%d", gotA, gotC)
	}
	if len(v.pending) != len(during) {
		t.Fatalf("待补齐列表长度不符: got=%d want=%d", len(v.pending), len(during))
	}
	t.Logf("待补齐列表: %+v", v.pending)
	t.Log("判定依据: 新事件同时生效于前台且进入待补齐列表（双写）")
}

// TestSwitchBackfillsPending 覆盖切换补齐：切换前先把待补齐列表
// 统一补齐到后台缓冲，切换后与朴素重放全量日志一致。
func TestSwitchBackfillsPending(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}, {"b", 2}, {"a", 3}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	replayAll(t, v)
	during := []Event{{"b", 5}, {"c", 4}}
	t.Logf("重放完成后、切换前到达事件: %+v", during)
	if err := v.Apply(during...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.Switch(); err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	got := v.Snapshot()
	want := naiveReplay(append(append([]Event{}, base...), during...))
	t.Logf("切换后前台: %v, 朴素重放基准: %v", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("切换补齐后结果与朴素重放不一致: got=%v want=%v", got, want)
	}
	if v.Rebuilding() {
		t.Fatal("切换后重建状态未清空")
	}
	t.Log("判定依据: 切换先补齐待补齐列表再原子换指针，结果 == 朴素重放全量日志")
}

// TestSwitchCompletesPartialReplay 覆盖切换时重放未完成的情况：
// 切换会先补完剩余历史事件再补齐待补齐列表，结果仍与朴素重放一致。
func TestSwitchCompletesPartialReplay(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}, {"b", 2}, {"c", 3}, {"a", 4}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	if _, _, err := v.ReplayNext(); err != nil {
		t.Fatalf("ReplayNext 失败: %v", err)
	}
	t.Log("只重放了 1/4 条历史事件即发起切换")
	during := []Event{{"b", 10}}
	if err := v.Apply(during...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.Switch(); err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	got := v.Snapshot()
	want := naiveReplay(append(append([]Event{}, base...), during...))
	t.Logf("切换后前台: %v, 朴素重放基准: %v", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("部分重放下切换结果与朴素重放不一致: got=%v want=%v", got, want)
	}
	t.Log("判定依据: 切换自动补完剩余重放并补齐待补齐列表，结果 == 朴素重放")
}

// TestAbortRebuild 覆盖中止：丢弃后台缓冲与待补齐列表，前台保持不变。
func TestAbortRebuild(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}, {"b", 2}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	replayAll(t, v)
	during := []Event{{"a", 5}}
	if err := v.Apply(during...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	before := v.Snapshot()
	if err := v.AbortRebuild(); err != nil {
		t.Fatalf("AbortRebuild 失败: %v", err)
	}
	after := v.Snapshot()
	t.Logf("中止前后台: %v, 中止后前台: %v", before, after)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("中止后前台发生变化: before=%v after=%v", before, after)
	}
	if v.Rebuilding() {
		t.Fatal("中止后重建状态未清空")
	}
	if v.back != nil || v.pending != nil {
		t.Fatal("中止后后台缓冲或待补齐列表未丢弃")
	}
	t.Log("判定依据: 中止只丢弃重建状态，前台视图不变")
}

// TestFrontServesDuringRebuild 覆盖重建期间读路径仍走前台视图。
func TestFrontServesDuringRebuild(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}, {"b", 2}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	replayAll(t, v)
	got := v.Snapshot()
	t.Logf("重建期间读到的视图: %v", got)
	if !reflect.DeepEqual(got, naiveReplay(base)) {
		t.Fatalf("重建期间读到的不是前台视图: got=%v", got)
	}
	if n, _ := v.Get("a"); n != 1 {
		t.Fatalf("重建期间 Get(a)=%d, 应为前台值 1", n)
	}
	t.Log("判定依据: 重建未完成切换前，所有读都来自前台缓冲")
}

// stateOf 抓取用于“状态不变”判定的全部可观测状态。
func stateOf(v *View) (map[string]int64, []Event, bool) {
	return v.Snapshot(), v.Log(), v.Rebuilding()
}

// TestInvalidInputsRejected 覆盖四类非法输入：非重建中切换/中止、
// 重建中重复开始、非重建中重放、非法事件（空键/零增量），
// 每类都有互不相同的可判定错误，且被拒后状态不变。
func TestInvalidInputsRejected(t *testing.T) {
	v := NewView()
	base := []Event{{"a", 1}}
	if err := v.Apply(base...); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}

	assertStateUnchanged := func(step string, beforeView map[string]int64, beforeLog []Event, beforeRebuilding bool) {
		t.Helper()
		gotView, gotLog, gotRebuilding := stateOf(v)
		if !reflect.DeepEqual(gotView, beforeView) || !reflect.DeepEqual(gotLog, beforeLog) || gotRebuilding != beforeRebuilding {
			t.Fatalf("%s 被拒后状态发生变化: view=%v log=%v rebuilding=%v", step, gotView, gotLog, gotRebuilding)
		}
		t.Logf("%s 被拒后状态不变: view=%v log=%v rebuilding=%v", step, gotView, gotLog, gotRebuilding)
	}

	snap, lg, rb := stateOf(v)

	t.Log("用例1: 非重建中执行切换")
	if err := v.Switch(); !errors.Is(err, ErrNotRebuilding) {
		t.Fatalf("期望 ErrNotRebuilding, 得到 %v", err)
	}
	assertStateUnchanged("非重建中切换(ErrNotRebuilding)", snap, lg, rb)

	t.Log("用例2: 非重建中执行中止")
	if err := v.AbortRebuild(); !errors.Is(err, ErrNotRebuilding) {
		t.Fatalf("期望 ErrNotRebuilding, 得到 %v", err)
	}
	assertStateUnchanged("非重建中中止(ErrNotRebuilding)", snap, lg, rb)

	t.Log("用例3: 非重建中执行重放")
	if _, _, err := v.ReplayNext(); !errors.Is(err, ErrReplayNotRebuilding) {
		t.Fatalf("期望 ErrReplayNotRebuilding, 得到 %v", err)
	}
	assertStateUnchanged("非重建中重放(ErrReplayNotRebuilding)", snap, lg, rb)

	t.Log("用例4: 重建中重复开始")
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}
	snap, lg, rb = stateOf(v)
	if err := v.BeginRebuild(); !errors.Is(err, ErrAlreadyRebuilding) {
		t.Fatalf("期望 ErrAlreadyRebuilding, 得到 %v", err)
	}
	assertStateUnchanged("重建中重复开始(ErrAlreadyRebuilding)", snap, lg, rb)
	if err := v.AbortRebuild(); err != nil {
		t.Fatalf("AbortRebuild 失败: %v", err)
	}

	t.Log("用例5: 非法事件（键为空）")
	snap, lg, rb = stateOf(v)
	if err := v.Apply(Event{"", 5}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("期望 ErrEmptyKey, 得到 %v", err)
	}
	assertStateUnchanged("空键事件(ErrEmptyKey)", snap, lg, rb)

	t.Log("用例6: 非法事件（增量为零）")
	if err := v.Apply(Event{"a", 0}); !errors.Is(err, ErrZeroDelta) {
		t.Fatalf("期望 ErrZeroDelta, 得到 %v", err)
	}
	assertStateUnchanged("零增量事件(ErrZeroDelta)", snap, lg, rb)

	t.Log("判定依据: 四类非法输入分别命中四个互不相同的哨兵错误，且状态不变")
}

// TestBatchAtomicity 覆盖批原子性：批内任一条被拒则整批不生效。
func TestBatchAtomicity(t *testing.T) {
	v := NewView()
	if err := v.Apply(Event{"a", 1}); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	beforeView, beforeLog, _ := stateOf(v)
	batch := []Event{{"b", 2}, {"c", 3}, {"bad", 0}, {"d", 4}}
	t.Logf("输入批: %+v (第3条增量为零，非法)", batch)
	if err := v.Apply(batch...); !errors.Is(err, ErrZeroDelta) {
		t.Fatalf("期望 ErrZeroDelta, 得到 %v", err)
	}
	gotView, gotLog, _ := stateOf(v)
	t.Logf("拒绝后视图: %v, 日志: %v", gotView, gotLog)
	if !reflect.DeepEqual(gotView, beforeView) || !reflect.DeepEqual(gotLog, beforeLog) {
		t.Fatalf("批内存在非法事件但部分生效: view=%v log=%v", gotView, gotLog)
	}
	t.Log("判定依据: 批先整体校验再整体生效，任一被拒则整批回滚（无部分写入）")
}

// TestConcurrentWriteRead 覆盖并发写入与并发读：
// 最终计数等于全部事件之和，且任一时刻读到的都是一致的完整视图。
func TestConcurrentWriteRead(t *testing.T) {
	v := NewView()
	const writers = 8
	const eventsPerWriter = 500
	const readers = 4

	var readersWg, writersWg sync.WaitGroup
	stop := make(chan struct{})

	for r := 0; r < readers; r++ {
		readersWg.Add(1)
		go func(id int) {
			defer readersWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := v.Snapshot()
				var total int64
				for key, n := range snap {
					if n <= 0 {
						t.Errorf("读者%d 读到非正计数: %s=%d（视图不一致）", id, key, n)
					}
					total += n
				}
				if total > writers*eventsPerWriter {
					t.Errorf("读者%d 读到总数 %d 超过已写入上限（视图不一致）", id, total)
				}
			}
		}(r)
	}

	for w := 0; w < writers; w++ {
		writersWg.Add(1)
		go func(id int) {
			defer writersWg.Done()
			key := fmt.Sprintf("key-%d", id%4)
			for i := 0; i < eventsPerWriter; i++ {
				if err := v.Apply(Event{key, 1}); err != nil {
					t.Errorf("写者%d Apply 失败: %v", id, err)
					return
				}
			}
		}(w)
	}

	writersWg.Wait()
	close(stop)
	readersWg.Wait()

	got := v.Snapshot()
	var total int64
	for _, n := range got {
		total += n
	}
	t.Logf("并发写入 %d 写者 x %d 条后最终视图: %v, 总数: %d", writers, eventsPerWriter, got, total)
	if total != writers*eventsPerWriter {
		t.Fatalf("最终总数不符: got=%d want=%d", total, writers*eventsPerWriter)
	}
	if got["key-0"] != got["key-1"] || got["key-1"] != got["key-2"] || got["key-2"] != got["key-3"] {
		t.Fatalf("各键计数不均衡，疑似丢事件: %v", got)
	}
	t.Log("判定依据: 最终计数 == 写入事件总数；读到的每个快照计数均为正且不超过已写上限")
}

// TestConcurrentRebuildSwitch 覆盖重建/切换与并发写入、并发读同时进行：
// 切换后前台必须与朴素重放全量日志一致。
func TestConcurrentRebuildSwitch(t *testing.T) {
	v := NewView()
	const writers = 4
	const eventsPerWriter = 200

	if err := v.Apply(Event{"seed", 1}); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if err := v.BeginRebuild(); err != nil {
		t.Fatalf("BeginRebuild 失败: %v", err)
	}

	var readersWg, writersWg sync.WaitGroup
	stop := make(chan struct{})
	readersWg.Add(1)
	go func() {
		defer readersWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = v.Snapshot()
		}
	}()

	for w := 0; w < writers; w++ {
		writersWg.Add(1)
		go func(id int) {
			defer writersWg.Done()
			for i := 0; i < eventsPerWriter; i++ {
				if err := v.Apply(Event{fmt.Sprintf("w%d", id), 1}); err != nil {
					t.Errorf("写者%d Apply 失败: %v", id, err)
					return
				}
			}
		}(w)
	}

	if _, _, err := v.ReplayNext(); err != nil {
		t.Fatalf("ReplayNext 失败: %v", err)
	}
	writersWg.Wait()
	close(stop)
	readersWg.Wait()

	if err := v.Switch(); err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	got := v.Snapshot()
	want := naiveReplay(v.Log())
	t.Logf("切换后前台: %v, 朴素重放全量日志: %v", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("并发重建切换结果与朴素重放不一致: got=%v want=%v", got, want)
	}
	t.Log("判定依据: 并发写入 + 重建 + 切换后，前台 == 朴素重放全量日志")
}
