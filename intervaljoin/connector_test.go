package intervaljoin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 测试辅助 ----

func mustNew(t *testing.T, max int) *Connector {
	t.Helper()
	c, err := New(Config{MaxRetainedPerSide: max})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return c
}

func proc(t *testing.T, c *Connector, s Side, e Event) []Pair {
	t.Helper()
	out, err := c.Process(s, &e)
	if err != nil {
		t.Fatalf("Process(%s,%+v): unexpected error: %v", s, e, err)
	}
	return out
}

func expectErrCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var je *JoinError
	if !errors.As(err, &je) {
		t.Fatalf("want *JoinError code %s, got %v", want, err)
	}
	if je.Code != want {
		t.Fatalf("want code %s, got %s (err=%v)", want, je.Code, err)
	}
}

func pairKey(p Pair) string {
	return fmt.Sprintf("%s:%d:%d", p.Key, p.LeftID, p.RightID)
}

func pairIDSet(ps []Pair) map[string]bool {
	m := make(map[string]bool, len(ps))
	for _, p := range ps {
		m[pairKey(p)] = true
	}
	return m
}

// snapshot 捕获用于“拒绝不改变状态”断言的完整可观察状态。
type snapshot struct {
	wmL, wmR     int64
	wmLOk, wmROk bool
	left, right  []RetainedEvent
	pairs        []Pair
}

func snap(c *Connector) snapshot {
	wmL, okL := c.Watermark(SideLeft)
	wmR, okR := c.Watermark(SideRight)
	return snapshot{
		wmL: wmL, wmR: wmR, wmLOk: okL, wmROk: okR,
		left:  c.Retained(SideLeft),
		right: c.Retained(SideRight),
		pairs: c.Pairs(),
	}
}

// ---- 1. 区间两端闭合：端点相交必须配对 ----

func TestClosedIntervalBoundaries(t *testing.T) {
	c := mustNew(t, 100)

	// 左事件 [0,10] 先到并保留。
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 10})

	// 右端点相切：右 [10,20]，Lo==左 Hi，闭合区间在 t=10 相交。
	got := proc(t, c, SideRight, Event{Key: "k", Lo: 10, Hi: 20})
	if len(got) != 1 || got[0].LeftID != 1 || got[0].RightID != 1 {
		t.Fatalf("closed boundary Lo==Hi should match, got %+v", got)
	}

	// 左端点相切：左 [20,30]，Lo==右 Hi(20)，闭合区间在 t=20 相交。
	got = proc(t, c, SideLeft, Event{Key: "k", Lo: 20, Hi: 30})
	if len(got) != 1 || got[0].LeftID != 2 || got[0].RightID != 1 {
		t.Fatalf("closed boundary Hi==Lo should match, got %+v", got)
	}

	// 点区间 [30,30] 与 [20,30] 在 t=30 闭合相交。
	got = proc(t, c, SideRight, Event{Key: "k", Lo: 30, Hi: 30})
	if len(got) != 1 {
		t.Fatalf("point interval touching endpoint should match, got %+v", got)
	}

	// 严格不相交（相邻但不接触）：右 [31,40] 与左 [20,30]，31>30，不匹配；
	// 与更早的左事件同样不相交。
	got = proc(t, c, SideRight, Event{Key: "k", Lo: 31, Hi: 40})
	if len(got) != 0 {
		t.Fatalf("non-overlapping intervals must not match, got %+v", got)
	}
}

func TestNoCrossKeyMatch(t *testing.T) {
	c := mustNew(t, 100)
	proc(t, c, SideLeft, Event{Key: "a", Lo: 0, Hi: 10})
	got := proc(t, c, SideRight, Event{Key: "b", Lo: 0, Hi: 10})
	if len(got) != 0 {
		t.Fatalf("events with different keys must not match, got %+v", got)
	}
}

// ---- 2. 每步清理的精确性 ----

func TestCleanupPrecisionPerStep(t *testing.T) {
	c := mustNew(t, 100)

	// L1=[0,5]（短，随后会被清）；L2=[0,10]（端点可活到右 wm=10）。
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 5})
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 10})

	// R1=[10,10]：与 L1 不相交（10>5），与 L2 在端点 10 闭合相交。
	got := proc(t, c, SideRight, Event{Key: "k", Lo: 10, Hi: 10})
	if len(got) != 1 || got[0].LeftID != 2 || got[0].RightID != 1 {
		t.Fatalf("R1 step: want single match with L2, got %+v", got)
	}
	// 本步右 wm=10：L1.Hi=5 < 10 必须删除；L2.Hi=10 == 10 必须保留（边界！）。
	left := c.Retained(SideLeft)
	if len(left) != 1 || left[0].ID != 2 {
		t.Fatalf("after R1: only L2 (Hi==wm) must survive, got %+v", left)
	}

	// L3=[10,10]：左 wm 推进到 10；与 R1 点区间在 t=10 闭合相交。
	got = proc(t, c, SideLeft, Event{Key: "k", Lo: 10, Hi: 10})
	if len(got) != 1 || got[0].LeftID != 3 || got[0].RightID != 1 {
		t.Fatalf("L3 step: want match L3-R1 at closed endpoint, got %+v", got)
	}

	// R2=[11,11]：右 wm=11；与任何保留左事件都不相交（左 Hi 最大为 10）。
	got = proc(t, c, SideRight, Event{Key: "k", Lo: 11, Hi: 11})
	if len(got) != 0 {
		t.Fatalf("R2 step: no overlap expected, got %+v", got)
	}
	// Hi=10 < wm=11：L2、L3 都必须被精确删除。
	if left := c.Retained(SideLeft); len(left) != 0 {
		t.Fatalf("after R2: left side must be empty, got %+v", left)
	}
	// 右自身：R1.Hi=10 < 左 wm(10)? 否，上一步保留；本步左 wm 仍为 10，
	// R1 仍保留；R2 也保留。
	if right := c.Retained(SideRight); len(right) != 2 {
		t.Fatalf("after R2: right side keeps R1,R2, got %+v", right)
	}

	// 最终配对集合必须恰好为 {(L2,R1),(L3,R1)}：每个事件对只输出一次，
	// 已清理的 L1 从未配对（不多对、不漏对）。
	want := map[string]bool{"k:2:1": true, "k:3:1": true}
	if got := pairIDSet(c.Pairs()); !reflect.DeepEqual(got, want) {
		t.Fatalf("final pairs = %v, want %v", got, want)
	}
}

func TestProcessOutputSortedByOppositeID(t *testing.T) {
	c := mustNew(t, 100)
	// 三条同键右事件先到并全部保留（左 wm 未推进，不清理右侧）。
	proc(t, c, SideRight, Event{Key: "k", Lo: 0, Hi: 100})
	proc(t, c, SideRight, Event{Key: "k", Lo: 0, Hi: 100})
	proc(t, c, SideRight, Event{Key: "k", Lo: 0, Hi: 100})
	// 一条左事件与三条都相交；本步输出必须按右编号 1,2,3 升序。
	got := proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 100})
	if len(got) != 3 {
		t.Fatalf("want 3 pairs, got %d (%+v)", len(got), got)
	}
	for i, p := range got {
		if p.RightID != int64(i+1) {
			t.Fatalf("pair %d RightID=%d, want %d", i, p.RightID, i+1)
		}
	}
}

// ---- 3. 各类非法输入 ----

func TestInvalidConfig(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		_, err := New(Config{MaxRetainedPerSide: n})
		expectErrCode(t, err, CodeInvalidParameter)
	}
}

func TestInvalidEvents(t *testing.T) {
	c := mustNew(t, 100)

	// nil 事件。
	if _, err := c.Process(SideLeft, nil); err == nil {
		t.Fatal("nil event must be rejected")
	} else {
		expectErrCode(t, err, CodeInvalidParameter)
	}

	// Lo > Hi。
	_, err := c.Process(SideLeft, &Event{Key: "k", Lo: 10, Hi: 9})
	expectErrCode(t, err, CodeInvalidParameter)

	// 空键（含纯空白也算空键之外的普通非空键，这里只测空串）。
	_, err = c.Process(SideLeft, &Event{Key: "", Lo: 0, Hi: 10})
	expectErrCode(t, err, CodeEmptyKey)

	// 非法 Side。
	_, err = c.Process(Side(42), &Event{Key: "k", Lo: 0, Hi: 10})
	expectErrCode(t, err, CodeInvalidParameter)
}

func TestTimeRegression(t *testing.T) {
	c := mustNew(t, 100)
	proc(t, c, SideLeft, Event{Key: "k", Lo: 5, Hi: 10})

	// 同一侧 Lo 倒退（即便 Hi 很大也必须拒绝）。
	_, err := c.Process(SideLeft, &Event{Key: "k", Lo: 4, Hi: 100})
	expectErrCode(t, err, CodeTimeRegression)

	// Lo 相等是非递减，合法。
	proc(t, c, SideLeft, Event{Key: "k", Lo: 5, Hi: 6})
	// 对侧水位线独立，右侧从 0 开始不构成倒退。
	proc(t, c, SideRight, Event{Key: "k", Lo: 0, Hi: 0})
	// 右侧自身随后倒退则拒绝。
	_, err = c.Process(SideRight, &Event{Key: "k", Lo: -1, Hi: 0})
	expectErrCode(t, err, CodeTimeRegression)
}

func TestRetentionLimitExceeded(t *testing.T) {
	c := mustNew(t, 2)
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 100})
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 100})
	// 对侧水位线从未推进，左侧无清理；第三条将使本步后保留数=3>2。
	_, err := c.Process(SideLeft, &Event{Key: "k", Lo: 0, Hi: 100})
	expectErrCode(t, err, CodeRetentionLimitExceeded)

	// 推进右水位线让左事件被清掉后，容量重新可用，操作可成功：
	// 证明超限拒绝不是“毒化”状态。
	proc(t, c, SideRight, Event{Key: "other", Lo: 101, Hi: 200})
	proc(t, c, SideLeft, Event{Key: "k", Lo: 101, Hi: 200})
}

// ---- 4. 被拒绝操作不改变任何可观察状态 ----

func TestRejectionLeavesStateUntouched(t *testing.T) {
	c := mustNew(t, 100)
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 10})
	proc(t, c, SideRight, Event{Key: "k", Lo: 0, Hi: 10}) // 产生一对

	// 校验类拒绝（nil、非法区间、空键、时间倒退）不得改变任何状态。
	base := snap(c)
	reject := func(s Side, e *Event) {
		t.Helper()
		if _, err := c.Process(s, e); err == nil {
			t.Fatalf("event %+v should have been rejected", e)
		}
	}
	reject(SideLeft, nil)
	reject(SideLeft, &Event{Key: "k", Lo: 9, Hi: 8})     // 非法区间
	reject(SideLeft, &Event{Key: "", Lo: 0, Hi: 10})     // 空键
	reject(SideLeft, &Event{Key: "k", Lo: -1, Hi: 100})  // 左 wm=0，倒退
	reject(Side(42), &Event{Key: "k", Lo: 0, Hi: 10})    // 非法侧
	reject(SideRight, &Event{Key: "k", Lo: -5, Hi: 100}) // 右 wm=0，倒退
	if got := snap(c); !reflect.DeepEqual(base, got) {
		t.Fatalf("validation rejection changed state:\nbase=%+v\ngot =%+v", base, got)
	}

	// 保留超限拒绝：填满上限后，再多一条必须被拒绝且状态逐字段不变；
	// 被拒绝的事件不得消耗编号（下一条成功事件的编号必须连续）。
	full := mustNew(t, 50)
	for i := 0; i < 50; i++ {
		proc(t, full, SideLeft, Event{Key: "k", Lo: 0, Hi: 1000})
	}
	if got := len(full.Retained(SideLeft)); got != 50 {
		t.Fatalf("setup: want 50 retained, got %d", got)
	}
	fullBase := snap(full)
	if _, err := full.Process(SideLeft, &Event{Key: "k", Lo: 0, Hi: 1000}); err == nil {
		t.Fatal("51st retained event must exceed the limit and be rejected")
	} else {
		expectErrCode(t, err, CodeRetentionLimitExceeded)
	}
	if got := snap(full); !reflect.DeepEqual(fullBase, got) {
		t.Fatalf("retention rejection changed state:\nbase=%+v\ngot =%+v", fullBase, got)
	}

	// 右水位线推进清理左侧后容量恢复，且下一条左事件编号连续（51，而非 52）。
	proc(t, full, SideRight, Event{Key: "z", Lo: 1001, Hi: 2000})
	out := proc(t, full, SideLeft, Event{Key: "z", Lo: 1001, Hi: 2000})
	if len(out) != 1 || out[0].LeftID != 51 || out[0].RightID != 1 {
		t.Fatalf("rejected event must not consume an ID, got %+v", out)
	}
}

// ---- 5. 同一输入序列反复计算得到完全相同的输出 ----

// 序列构造时保证每侧 Lo 非递减（L: 0,0,10,16,16；R: 5,5,15,15,21）。
func scenarioEvents() []struct {
	side Side
	e    Event
} {
	return []struct {
		side Side
		e    Event
	}{
		{SideLeft, Event{Key: "a", Lo: 0, Hi: 10}},
		{SideRight, Event{Key: "a", Lo: 5, Hi: 15}},
		{SideLeft, Event{Key: "b", Lo: 0, Hi: 3}},
		{SideRight, Event{Key: "b", Lo: 5, Hi: 8}},
		{SideLeft, Event{Key: "a", Lo: 10, Hi: 10}},
		{SideRight, Event{Key: "a", Lo: 15, Hi: 20}},
		{SideLeft, Event{Key: "a", Lo: 16, Hi: 18}},
		{SideRight, Event{Key: "b", Lo: 15, Hi: 15}},
		{SideLeft, Event{Key: "b", Lo: 16, Hi: 17}},
		{SideRight, Event{Key: "a", Lo: 21, Hi: 30}},
	}
}

func runScenario(max int) ([][]Pair, snapshot) {
	c, _ := New(Config{MaxRetainedPerSide: max})
	var steps [][]Pair
	for _, x := range scenarioEvents() {
		out, err := c.Process(x.side, &x.e)
		if err != nil {
			panic(err)
		}
		steps = append(steps, out)
	}
	return steps, snap(c)
}

func TestDeterministicAcrossRuns(t *testing.T) {
	baseSteps, baseSnap := runScenario(100)
	for i := 0; i < 30; i++ {
		steps, s := runScenario(100)
		if !reflect.DeepEqual(baseSteps, steps) {
			t.Fatalf("run %d: per-step outputs differ", i)
		}
		if !reflect.DeepEqual(baseSnap, s) {
			t.Fatalf("run %d: final state differs:\n%+v\n%+v", i, baseSnap, s)
		}
	}
}

// ---- 6. 并发只读一致性（go test -race 下验证无撕裂、无竞态）----

func TestConcurrentReadersConsistency(t *testing.T) {
	c := mustNew(t, 1000)
	var stop atomic.Bool
	var wg sync.WaitGroup

	// 单个写者串行推进两条流。
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := int64(0)
		for !stop.Load() {
			i++
			lo := i
			ev := Event{Key: fmt.Sprintf("k%d", i%5), Lo: lo, Hi: lo + 3}
			if _, err := c.Process(Side(i%2), &ev); err != nil {
				t.Errorf("writer: %v", err)
				return
			}
			time.Sleep(time.Microsecond)
		}
	}()

	// 多个读者：每个快照必须自洽，且配对集合相对上一次只增不减（只追加语义），
	// 任何撕裂/字段错配都会违反该性质或触发 race 报告。
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var prev map[string]Pair
			for !stop.Load() {
				ps := c.Pairs()
				cur := make(map[string]Pair, len(ps))
				for _, p := range ps {
					if p.LeftLo > p.LeftHi || p.RightLo > p.RightHi || p.Key == "" {
						t.Errorf("torn/inconsistent pair: %+v", p)
						return
					}
					cur[pairKey(p)] = p
				}
				// Pairs 必须按 (LeftID,RightID) 排序。
				if !sort.SliceIsSorted(ps, func(i, j int) bool {
					if ps[i].LeftID != ps[j].LeftID {
						return ps[i].LeftID < ps[j].LeftID
					}
					return ps[i].RightID < ps[j].RightID
				}) {
					t.Errorf("Pairs not sorted: %+v", ps)
					return
				}
				if prev != nil {
					for k, p := range prev {
						q, ok := cur[k]
						if !ok || !reflect.DeepEqual(p, q) {
							t.Errorf("pair %s disappeared or changed: before=%+v after=%+v", k, p, q)
							return
						}
					}
				}
				prev = cur
				_ = c.Retained(SideLeft)
				_, _ = c.Watermark(SideRight)
			}
		}()
	}

	time.Sleep(120 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// ---- 7. 日志：输入、输出配对与判定依据 ----

func TestLoggingIncludesInputsOutputsAndDecisions(t *testing.T) {
	var buf bytes.Buffer
	prev := logger
	SetLogger(&buf)
	t.Cleanup(func() { SetLogger(os.Stderr); _ = prev })

	c := mustNew(t, 100)
	proc(t, c, SideLeft, Event{Key: "k", Lo: 0, Hi: 10})
	proc(t, c, SideRight, Event{Key: "k", Lo: 10, Hi: 20}) // 端点闭合匹配
	proc(t, c, SideLeft, Event{Key: "k", Lo: 21, Hi: 30})  // 触发对 R1 的清理判定

	log := buf.String()
	for _, want := range []string{
		"input",  // 打印输入
		"output", // 打印输出配对数与保留数
		"match?", // 判定依据
		"=> true",
		"=> false",
		"cleanup", // 清理判定
		"removed",
		"kept",
		"[0,10]",
		"[10,20]",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
