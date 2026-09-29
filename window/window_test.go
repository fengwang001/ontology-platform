package window

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func logBatch(t *testing.T, label string, events []Event, changes []Change, why string) {
	t.Helper()
	t.Logf("%s 事件=%v", label, events)
	for _, c := range changes {
		t.Logf("  变更 #%d 键=%s 窗口=[%d,%d) 触发=%s 计数=%d",
			c.Seq, c.Key, c.WindowStart, c.WindowEnd, c.Type, c.Count)
	}
	t.Logf("  判定依据: %s", why)
}

func mustEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	return eng
}

func mustIngest(t *testing.T, eng *Engine, events ...Event) []Change {
	t.Helper()
	changes, err := eng.Ingest(events)
	if err != nil {
		t.Fatalf("Ingest 失败: %v", err)
	}
	return changes
}

func typeList(changes []Change) []TriggerType {
	out := make([]TriggerType, len(changes))
	for i, c := range changes {
		out[i] = c.Type
	}
	return out
}

// 早触发：计数每达阈值整数倍输出快照且不清零。
func TestEarlyTriggerDoesNotReset(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 2, WatermarkDelay: 0, AllowedLateness: 5, MaxOpenWindows: 8}
	eng := mustEngine(t, cfg)

	b1 := []Event{{Key: "a", Timestamp: 1}, {Key: "a", Timestamp: 2}}
	changes := mustIngest(t, eng, b1...)
	logBatch(t, "批1", b1, changes, "计数达到 EarlyEvery=2 的 1 倍，输出 EARLY(2)")
	if len(changes) != 1 || changes[0].Type != TriggerEarly || changes[0].Count != 2 {
		t.Fatalf("期望 1 条 EARLY(2)，实际 %+v", changes)
	}

	b2 := []Event{{Key: "a", Timestamp: 3}, {Key: "a", Timestamp: 4}}
	changes = mustIngest(t, eng, b2...)
	logBatch(t, "批2", b2, changes, "若早触发清零则计数到不了 4；输出 EARLY(4) 证明计数未清零")
	if len(changes) != 1 || changes[0].Type != TriggerEarly || changes[0].Count != 4 {
		t.Fatalf("期望 1 条 EARLY(4)（不清零），实际 %+v", changes)
	}

	b3 := []Event{{Key: "a", Timestamp: 5}}
	changes = mustIngest(t, eng, b3...)
	logBatch(t, "批3", b3, changes, "计数=5 非 2 的整数倍，不触发")
	if len(changes) != 0 {
		t.Fatalf("计数 5 不应触发，实际 %+v", changes)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 并发：查询、丢弃数与自检可并发调用，并发读取的视图逐字段相同，水位线不回退。
func TestConcurrentReadsConsistent(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 2, WatermarkDelay: 3, AllowedLateness: 5, MaxOpenWindows: 64}
	eng := mustEngine(t, cfg)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写入方：持续 ingest，并断言水位线单调不退。
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		rng := rand.New(rand.NewSource(7))
		lastWM := int64(math.MinInt64)
		for i := 0; i < 200; i++ {
			ev := Event{Key: fmt.Sprintf("k%d", rng.Intn(4)), Timestamp: int64(i * 2)}
			if _, err := eng.Ingest([]Event{ev}); err != nil {
				t.Errorf("Ingest 失败: %v", err)
				return
			}
			wm := eng.Snapshot().Watermark
			if wm < lastWM {
				t.Errorf("水位线回退: %d -> %d", lastWM, wm)
				return
			}
			lastWM = wm
		}
	}()

	// 读取方：并发 Snapshot/Dropped/SelfCheck，同一时刻两个快照必须逐字段相同。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s1 := eng.Snapshot()
				s2 := eng.Snapshot()
				if !reflect.DeepEqual(s1, s2) && s1.Watermark == s2.Watermark && s1.MaxEventTime == s2.MaxEventTime {
					t.Errorf("同水位线下两次快照不一致")
					return
				}
				_ = eng.Dropped()
				if err := eng.SelfCheck(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("并发读写完成，最终水位线=%d，丢弃=%d，判定依据: 水位线单调且并发自检全部通过",
		eng.Snapshot().Watermark, eng.Dropped())
}

// 批量重算核对：随机事件流下，变更日志汇总的最终计数与独立重算模型一致。
func TestBatchRecomputeCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		cfg := Config{WindowSize: 10, EarlyEvery: 2, WatermarkDelay: 3, AllowedLateness: 5, MaxOpenWindows: 128}
		eng := mustEngine(t, cfg)
		rng := rand.New(rand.NewSource(seed))

		var all []Event
		var changelog []Change
		for batch := 0; batch < 10; batch++ {
			n := rng.Intn(20) + 1
			events := make([]Event, n)
			for i := range events {
				events[i] = Event{
					Key:       fmt.Sprintf("k%d", rng.Intn(5)),
					Timestamp: int64(rng.Intn(400) - 100), // 含负时间戳与迟到
				}
			}
			changes, err := eng.Ingest(events)
			if err != nil {
				t.Fatalf("seed=%d 批 %d 被拒: %v", seed, batch, err)
			}
			all = append(all, events...)
			changelog = append(changelog, changes...)
		}

		if err := eng.SelfCheck(); err != nil {
			t.Fatalf("seed=%d 自检失败: %v", seed, err)
		}

		got := eng.FinalCounts()
		want := RecomputeFinalCounts(all, cfg)
		toMap := func(fs []FinalCount) map[WindowKey]int64 {
			m := make(map[WindowKey]int64, len(fs))
			for _, f := range fs {
				m[WindowKey{Key: f.Key, Start: f.WindowStart}] = f.Count
			}
			return m
		}
		gotMap, wantMap := toMap(got), toMap(want)
		if !reflect.DeepEqual(gotMap, wantMap) {
			t.Fatalf("seed=%d 最终计数不一致:\n引擎=%v\n重算=%v", seed, gotMap, wantMap)
		}
		t.Logf("seed=%d 事件=%d 变更=%d 窗口=%d 丢弃=%d 最终计数逐窗口一致",
			seed, len(all), len(changelog), len(gotMap), eng.Dropped())
	}
}

// 负时间戳：窗口按数学向下取整切分，左闭右开。
func TestNegativeTimestamps(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 100, WatermarkDelay: 0, AllowedLateness: 0, MaxOpenWindows: 8}
	eng := mustEngine(t, cfg)

	b1 := []Event{{Key: "a", Timestamp: -11}, {Key: "a", Timestamp: -10}, {Key: "a", Timestamp: -1}}
	changes := mustIngest(t, eng, b1...)
	logBatch(t, "批1", b1, changes,
		"-11∈[-20,-10)，-10∈[-10,0)，-1∈[-10,0)；水位线推进到 -10 时 [-20,-10) 准点输出 1 并清除（迟到上限 0）")
	snap := eng.Snapshot()
	w := snap.OpenWindows[WindowKey{Key: "a", Start: -10}]
	if w.Count != 2 || w.Start != -10 || w.End != 0 {
		t.Fatalf("期望窗口 [-10,0) 计数 2，实际 %+v", w)
	}
	if len(changes) != 1 || changes[0].Type != TriggerOnTime || changes[0].WindowStart != -20 || changes[0].Count != 1 {
		t.Fatalf("期望 ON_TIME([-20,-10),1)，实际 %+v", changes)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 非法配置：各类非法参数均可区分地拒绝。
func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{WindowSize: 0, EarlyEvery: 1, MaxOpenWindows: 1},
		{WindowSize: -5, EarlyEvery: 1, MaxOpenWindows: 1},
		{WindowSize: 10, EarlyEvery: 0, MaxOpenWindows: 1},
		{WindowSize: 10, EarlyEvery: 1, WatermarkDelay: -1, MaxOpenWindows: 1},
		{WindowSize: 10, EarlyEvery: 1, AllowedLateness: -1, MaxOpenWindows: 1},
		{WindowSize: 10, EarlyEvery: 1, MaxOpenWindows: 0},
	}
	for i, cfg := range cases {
		_, err := NewEngine(cfg)
		var re *RejectError
		if !errors.As(err, &re) || re.Reason != ReasonInvalidConfig {
			t.Fatalf("用例 %d 期望 INVALID_CONFIG，实际 %v", i, err)
		}
		t.Logf("用例 %d 配置=%+v 拒绝原因=%s 明细=%s", i, cfg, re.Reason, re.Detail)
	}
}

// 批量原子性：批内任一条被拒（空键/窗口数超限），整批不生效、状态不变。
func TestBatchAtomicRejection(t *testing.T) {
	// WatermarkDelay 足够大，保证测试期间水位线不触发任何清除。
	cfg := Config{WindowSize: 10, EarlyEvery: 2, WatermarkDelay: 1000, AllowedLateness: 5, MaxOpenWindows: 2}
	eng := mustEngine(t, cfg)

	base := []Event{{Key: "a", Timestamp: 1}}
	mustIngest(t, eng, base...)
	before := eng.Snapshot()

	// 空键拒绝：批内前面的事件也不得生效。
	badKey := []Event{{Key: "a", Timestamp: 2}, {Key: "", Timestamp: 3}}
	_, err := eng.Ingest(badKey)
	var re *RejectError
	if !errors.As(err, &re) || re.Reason != ReasonEmptyKey {
		t.Fatalf("期望 EMPTY_KEY，实际 %v", err)
	}
	logBatch(t, "空键批", badKey, nil, fmt.Sprintf("拒绝原因=%s，批内首条 a@2 不得生效", re.Reason))
	after := eng.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("空键拒绝后状态发生变化:\n前=%+v\n后=%+v", before, after)
	}

	// 窗口数超限拒绝：已有 1 个窗口，批内再开 2 个新窗口即超上限 2。
	tooMany := []Event{{Key: "a", Timestamp: 15}, {Key: "a", Timestamp: 25}}
	_, err = eng.Ingest(tooMany)
	if !errors.As(err, &re) || re.Reason != ReasonTooManyWindows {
		t.Fatalf("期望 TOO_MANY_WINDOWS，实际 %v", err)
	}
	logBatch(t, "超限批", tooMany, nil, fmt.Sprintf("拒绝原因=%s，批内首个新窗口 [10,20) 不得生效", re.Reason))
	after = eng.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("超限拒绝后状态发生变化:\n前=%+v\n后=%+v", before, after)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 迟到触发：准点后未超上限的迟到事件先撤回旧值再发新值。
func TestLateRetractThenUpdate(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 100, WatermarkDelay: 0, AllowedLateness: 5, MaxOpenWindows: 8}
	eng := mustEngine(t, cfg)

	b1 := []Event{{Key: "a", Timestamp: 1}, {Key: "a", Timestamp: 12}}
	changes := mustIngest(t, eng, b1...)
	logBatch(t, "批1", b1, changes, "水位线=12，窗口 [0,10) 准点输出 1；[10,20) 计数 1")
	if len(changes) != 1 || changes[0].Type != TriggerOnTime || changes[0].Count != 1 {
		t.Fatalf("期望 ON_TIME(1)，实际 %+v", changes)
	}

	b2 := []Event{{Key: "a", Timestamp: 3}}
	changes = mustIngest(t, eng, b2...)
	logBatch(t, "批2", b2, changes,
		"水位线 12 < 右边界 10 + 迟到上限 5 = 15，接受；先 LATE_RETRACT(1) 再 LATE_UPDATE(2)")
	want := []Change{
		{Key: "a", WindowStart: 0, WindowEnd: 10, Type: TriggerLateRetract, Count: 1},
		{Key: "a", WindowStart: 0, WindowEnd: 10, Type: TriggerLateUpdate, Count: 2},
	}
	if len(changes) != 2 {
		t.Fatalf("期望 2 条迟到变更，实际 %+v", changes)
	}
	for i, w := range want {
		got := changes[i]
		if got.Key != w.Key || got.WindowStart != w.WindowStart || got.WindowEnd != w.WindowEnd ||
			got.Type != w.Type || got.Count != w.Count {
			t.Fatalf("第 %d 条期望 %+v，实际 %+v", i, w, got)
		}
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 清除时机：水位线越过右边界+迟到上限后窗口状态清除，之后迟到事件被丢弃计数。
func TestCleanupAndDropAfterLateness(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 100, WatermarkDelay: 0, AllowedLateness: 5, MaxOpenWindows: 8}
	eng := mustEngine(t, cfg)

	b1 := []Event{{Key: "a", Timestamp: 1}, {Key: "a", Timestamp: 14}}
	mustIngest(t, eng, b1...)
	snap := eng.Snapshot()
	if _, ok := snap.OpenWindows[WindowKey{Key: "a", Start: 0}]; !ok {
		t.Fatalf("水位线 14 < 15，窗口 [0,10) 状态应保留")
	}
	t.Logf("水位线=14 < 10+5=15，窗口 [0,10) 状态保留，判定依据: 清除条件为水位线 >= 右边界+迟到上限")

	b2 := []Event{{Key: "a", Timestamp: 15}}
	changes := mustIngest(t, eng, b2...)
	logBatch(t, "批2", b2, changes, "水位线=15 >= 10+5，窗口 [0,10) 状态清除")
	snap = eng.Snapshot()
	if _, ok := snap.OpenWindows[WindowKey{Key: "a", Start: 0}]; ok {
		t.Fatalf("水位线 15 >= 15，窗口 [0,10) 应已清除")
	}

	b3 := []Event{{Key: "a", Timestamp: 2}}
	changes = mustIngest(t, eng, b3...)
	logBatch(t, "批3", b3, changes, "窗口已清除，迟到事件丢弃并计数，无变更日志")
	if len(changes) != 0 {
		t.Fatalf("超上限迟到应丢弃且无变更，实际 %+v", changes)
	}
	if got := eng.Dropped(); got != 1 {
		t.Fatalf("期望丢弃数 1，实际 %d", got)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 准点触发：水位线达到右边界时输出最终计数，且每窗口至多一次。
func TestOnTimeFiresOnce(t *testing.T) {
	cfg := Config{WindowSize: 10, EarlyEvery: 100, WatermarkDelay: 0, AllowedLateness: 5, MaxOpenWindows: 8}
	eng := mustEngine(t, cfg)

	b1 := []Event{{Key: "a", Timestamp: 1}, {Key: "a", Timestamp: 9}}
	changes := mustIngest(t, eng, b1...)
	logBatch(t, "批1", b1, changes, "EarlyEvery=100 不早触发；水位线=9 未达右边界 10")
	if len(changes) != 0 {
		t.Fatalf("水位线 9 未达右边界 10，不应触发，实际 %+v", changes)
	}

	b2 := []Event{{Key: "b", Timestamp: 10}}
	changes = mustIngest(t, eng, b2...)
	logBatch(t, "批2", b2, changes, "水位线推进到 10 >= 右边界 10，窗口 [0,10) 准点输出最终计数 2")
	if len(changes) != 1 || changes[0].Type != TriggerOnTime || changes[0].Count != 2 ||
		changes[0].Key != "a" || changes[0].WindowStart != 0 || changes[0].WindowEnd != 10 {
		t.Fatalf("期望 ON_TIME(a,[0,10),2)，实际 %+v", changes)
	}

	b3 := []Event{{Key: "b", Timestamp: 20}, {Key: "b", Timestamp: 30}}
	changes = mustIngest(t, eng, b3...)
	logBatch(t, "批3", b3, changes, "水位线继续前进，窗口 [0,10) 不得重复准点触发（去重）")
	for _, c := range changes {
		if c.Type == TriggerOnTime && c.WindowStart == 0 && c.Key == "a" {
			t.Fatalf("窗口 [0,10) 准点触发重复: %+v", changes)
		}
	}
	onTime := 0
	for _, c := range changes {
		if c.Type == TriggerOnTime {
			onTime++
		}
	}
	if onTime != 2 {
		t.Fatalf("期望批3触发 [10,20) 与 [20,30) 两个准点，实际 %d 个: %+v", onTime, changes)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}
