package temporal

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ---- 基础区间语义：左闭右开、末版本到无穷、边界相等 ----

func TestIntervalSemanticsAndBoundary(t *testing.T) {
	j := NewJoiner(0)
	mustApply(t, j, Version{Key: "k", EffectiveAt: 1, Value: "a"})
	mustApply(t, j, Version{Key: "k", EffectiveAt: 10, Value: "b"})

	// 水位线仍为 -Inf，事件全部需要缓冲（包括恰好等于生效起点的）。
	mustBuffer(t, j, Event{Key: "k", EventTime: 1, Payload: "e1"})
	mustBuffer(t, j, Event{Key: "k", EventTime: 10})
	mustBuffer(t, j, Event{Key: "k", EventTime: 100})

	out, err := j.AdvanceWatermark(100)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	// 区间 [1,10) 与 [10,+Inf)；事件时间恰好等于生效起点 => 命中该版本（左闭）。
	want := []Result{
		{Seq: 0, Kind: KindHit, Key: "k", EventTime: 1, EffectiveAt: 1, Value: "a"},
		{Seq: 1, Kind: KindHit, Key: "k", EventTime: 10, EffectiveAt: 10, Value: "b"},
		{Seq: 2, Kind: KindHit, Key: "k", EventTime: 100, EffectiveAt: 10, Value: "b"},
	}
	assertResults(t, out, want)

	// 生效起点早于第一个版本：未命中（不存在 <= 事件时间的起点）。
	expectResult(t, j, Event{Key: "k", EventTime: 0}, KindMiss, 0, "", StatusResolved)
}

func TestEventAtWatermarkResolvesImmediately(t *testing.T) {
	j := NewJoiner(0)
	mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "v"})
	if _, err := j.AdvanceWatermark(5); err != nil {
		t.Fatal(err)
	}
	// 事件时间恰好等于水位线：立即确定，且恰好命中起点为 5 的版本。
	expectResult(t, j, Event{Key: "k", EventTime: 5}, KindHit, 5, "v", StatusResolved)
}

// ---- 墓碑：区间内无值；墓碑之后可以重新有值 ----

func TestTombstoneQuery(t *testing.T) {
	j := NewJoiner(0)
	mustApply(t, j, Version{Key: "k", EffectiveAt: 1, Value: "a"})
	mustApply(t, j, Version{Key: "k", EffectiveAt: 10, Tombstone: true})
	mustApply(t, j, Version{Key: "k", EffectiveAt: 20, Value: "c"})

	events := []Event{
		{Key: "k", EventTime: 9, Payload: "before"},     // 命中 a
		{Key: "k", EventTime: 10, Payload: "atTomb"},    // 恰好墓碑起点 => 未命中
		{Key: "k", EventTime: 19, Payload: "inTomb"},    // 墓碑区间内 => 未命中
		{Key: "k", EventTime: 20, Payload: "afterTomb"}, // 墓碑之后重新有值 => 命中 c
		{Key: "other", EventTime: 20, Payload: "noKey"}, // 从无版本 => 未命中
	}
	for _, e := range events {
		mustBuffer(t, j, e)
	}
	out, err := j.AdvanceWatermark(20)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Seq: 0, Kind: KindHit, Key: "k", EventTime: 9, EffectiveAt: 1, Value: "a"},
		{Seq: 1, Kind: KindMiss, Key: "k", EventTime: 10},
		{Seq: 2, Kind: KindMiss, Key: "k", EventTime: 19},
		{Seq: 3, Kind: KindHit, Key: "k", EventTime: 20, EffectiveAt: 20, Value: "c"},
		{Seq: 4, Kind: KindMiss, Key: "other", EventTime: 20},
	}
	assertResults(t, out, want)
}

// ---- 同一生效起点的覆盖：值覆盖值、值覆盖墓碑、墓碑覆盖值 ----

// 同一生效起点的覆盖语义：
// 版本在其生效起点被水位线“封存”前可以被同点反复覆盖，事件（事件时间不小于
// 该起点）只能在封存之后确定，因此观察到的永远是封存前的最后一次覆盖。
// 三种覆盖方向分别用独立场景验证。
func TestSameEffectiveAtOverwrite(t *testing.T) {
	// 值覆盖值。
	t.Run("valueOverwritesValue", func(t *testing.T) {
		j := NewJoiner(0)
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a"})
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a2"})
		mustBuffer(t, j, Event{Key: "k", EventTime: 7, Payload: "e"})
		out := j.Drain()
		assertResults(t, out, []Result{
			{Seq: 0, Kind: KindHit, Key: "k", EventTime: 7, Payload: "e", EffectiveAt: 5, Value: "a2"},
		})
	})

	// 墓碑覆盖值。
	t.Run("tombstoneOverwritesValue", func(t *testing.T) {
		j := NewJoiner(0)
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a"})
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Tombstone: true})
		mustBuffer(t, j, Event{Key: "k", EventTime: 7, Payload: "e"})
		out := j.Drain()
		assertResults(t, out, []Result{
			{Seq: 0, Kind: KindMiss, Key: "k", EventTime: 7, Payload: "e"},
		})
	})

	// 值覆盖墓碑（墓碑可被撤销）。
	t.Run("valueOverwritesTombstone", func(t *testing.T) {
		j := NewJoiner(0)
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Tombstone: true})
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a3"})
		mustBuffer(t, j, Event{Key: "k", EventTime: 9, Payload: "e"})
		out := j.Drain()
		assertResults(t, out, []Result{
			{Seq: 0, Kind: KindHit, Key: "k", EventTime: 9, Payload: "e", EffectiveAt: 5, Value: "a3"},
		})
	})

	// 覆盖不影响后续起点；封存（水位线到达）后同点覆盖按迟到拒绝。
	t.Run("doesNotAffectLaterStartAndSealed", func(t *testing.T) {
		j := NewJoiner(0)
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a"})
		mustApply(t, j, Version{Key: "k", EffectiveAt: 10, Value: "b"})
		mustApply(t, j, Version{Key: "k", EffectiveAt: 5, Value: "a2"})
		mustBuffer(t, j, Event{Key: "k", EventTime: 9, Payload: "e1"})
		mustBuffer(t, j, Event{Key: "k", EventTime: 10, Payload: "e2"})
		// 水位线到达 5：起点 5 被封存，此后同点覆盖即迟到。
		if _, err := j.AdvanceWatermark(9); err != nil {
			t.Fatal(err)
		}
		if err := j.ApplyVersion(Version{Key: "k", EffectiveAt: 5, Value: "aX"}); !errors.Is(err, ErrLateVersion) {
			t.Fatalf("sealed overwrite: want ErrLateVersion, got %v", err)
		}
		out := j.Drain()
		assertResults(t, out, []Result{
			{Seq: 1, Kind: KindHit, Key: "k", EventTime: 10, Payload: "e2", EffectiveAt: 10, Value: "b"},
		})
		all := j.Results()
		assertResults(t, all, []Result{
			{Seq: 0, Kind: KindHit, Key: "k", EventTime: 9, Payload: "e1", EffectiveAt: 5, Value: "a2"},
			{Seq: 1, Kind: KindHit, Key: "k", EventTime: 10, Payload: "e2", EffectiveAt: 10, Value: "b"},
		})
	})
}

// ---- 迟到版本判定：生效起点严格晚于水位线才接受 ----

func TestLateVersionRejected(t *testing.T) {
	j := NewJoiner(0)
	mustApply(t, j, Version{Key: "k", EffectiveAt: 1, Value: "old"})
	if _, err := j.AdvanceWatermark(10); err != nil {
		t.Fatal(err)
	}
	before := j.Results()

	// 恰好等于水位线 => 迟到拒绝；早于同理。
	if err := j.ApplyVersion(Version{Key: "k", EffectiveAt: 10, Value: "x"}); !errors.Is(err, ErrLateVersion) {
		t.Fatalf("eff==wm: want ErrLateVersion, got %v", err)
	}
	if err := j.ApplyVersion(Version{Key: "k", EffectiveAt: 9, Value: "x"}); !errors.Is(err, ErrLateVersion) {
		t.Fatalf("eff<wm: want ErrLateVersion, got %v", err)
	}
	// 严格大于 => 接受。
	if err := j.ApplyVersion(Version{Key: "k", EffectiveAt: 11, Value: "new"}); err != nil {
		t.Fatalf("eff>wm should be accepted: %v", err)
	}
	// 拒绝不改变水位线与已输出。
	if j.Watermark() != 10 {
		t.Fatalf("watermark changed: %d", j.Watermark())
	}
	assertResults(t, j.Results(), before)

	// 迟到版本被拒后，事件查询结果必须稳定：事件时间 10 仍看到 1 号版本。
	expectResult(t, j, Event{Key: "k", EventTime: 10}, KindHit, 1, "old", StatusResolved)
}

// ---- 各类非法输入 ----

func TestEmptyKeyRejected(t *testing.T) {
	j := NewJoiner(0)
	if err := j.ApplyVersion(Version{Key: "", EffectiveAt: 1}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("version empty key: %v", err)
	}
	if _, _, err := j.ProcessEvent(Event{Key: "", EventTime: 1}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("event empty key: %v", err)
	}
	if len(j.Results()) != 0 || j.Watermark() != math.MinInt64 {
		t.Fatal("rejected empty-key ops must leave state untouched")
	}
}

func TestWatermarkRegressionRejectedAndNoOutput(t *testing.T) {
	j := NewJoiner(0)
	if _, err := j.AdvanceWatermark(10); err != nil {
		t.Fatal(err)
	}
	// 缓冲一个事件时间为 5 的事件不可能（会立即确定）；改为缓冲未来事件，
	// 再尝试回退，该次调用不得输出任何结果。
	mustBuffer(t, j, Event{Key: "k", EventTime: 20})
	out, err := j.AdvanceWatermark(9)
	if !errors.Is(err, ErrWatermarkRegression) {
		t.Fatalf("regression: err=%v out=%v", err, out)
	}
	if out != nil {
		t.Fatalf("regression call produced outputs: %v", out)
	}
	if j.Watermark() != 10 {
		t.Fatalf("watermark changed by rejected regression: %d", j.Watermark())
	}
	if bufferedCount(j) != 1 {
		t.Fatalf("buffered event touched by rejected regression: %d", bufferedCount(j))
	}
	// 合法推进后该事件正常输出一次。
	out, err = j.AdvanceWatermark(20)
	if err != nil || len(out) != 1 {
		t.Fatalf("valid advance: out=%v err=%v", out, err)
	}
}

func TestBufferLimitExceeded(t *testing.T) {
	j := NewJoiner(1)
	mustBuffer(t, j, Event{Key: "k", EventTime: 100})
	// 缓冲满：新的未来事件被拒。
	if _, _, err := j.ProcessEvent(Event{Key: "k", EventTime: 101}); !errors.Is(err, ErrBufferLimitExceeded) {
		t.Fatalf("second buffer: want ErrBufferLimitExceeded, got %v", err)
	}
	// 立即确定的事件不占用缓冲，即使缓冲已满也应被接受。
	mustApply(t, j, Version{Key: "k", EffectiveAt: 1, Value: "v"})
	if _, err := j.AdvanceWatermark(50); err != nil {
		t.Fatal(err)
	}
	st, r, err := j.ProcessEvent(Event{Key: "k", EventTime: 50})
	if err != nil || st != StatusResolved || r == nil || r.Kind != KindHit {
		t.Fatalf("immediate event while buffer full: st=%d r=%v err=%v", st, r, err)
	}
	// 推进水位线到 100，缓冲中的事件最终确定、槽位腾出。
	out, err := j.AdvanceWatermark(100)
	if err != nil || len(out) != 1 {
		t.Fatalf("advance to 100: out=%v err=%v", out, err)
	}
	mustBuffer(t, j, Event{Key: "k", EventTime: 101})
}

// 拒绝操作不得改变版本表、水位线、缓冲与已输出结果。
func TestRejectionsDoNotMutateState(t *testing.T) {
	j := NewJoiner(2)
	mustApply(t, j, Version{Key: "k", EffectiveAt: 10, Value: "v"})
	mustBuffer(t, j, Event{Key: "k", EventTime: 100})

	if _, err := j.AdvanceWatermark(50); err != nil {
		t.Fatal(err)
	}
	emittedBefore := len(j.Results()) // 事件 100 仍缓冲，已输出 0

	// 迟到版本 / 空键版本 / 空键事件 / 回退 / 缓冲超限 —— 全部拒绝。
	if err := j.ApplyVersion(Version{Key: "k", EffectiveAt: 50, Value: "x"}); !errors.Is(err, ErrLateVersion) {
		t.Fatalf("late: %v", err)
	}
	if err := j.ApplyVersion(Version{Key: "", EffectiveAt: 51}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty version: %v", err)
	}
	if _, _, err := j.ProcessEvent(Event{Key: "", EventTime: 1000}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty event: %v", err)
	}
	if _, err := j.AdvanceWatermark(49); !errors.Is(err, ErrWatermarkRegression) {
		t.Fatalf("regression: %v", err)
	}
	mustBuffer(t, j, Event{Key: "k", EventTime: 1000}) // 占满缓冲
	if _, _, err := j.ProcessEvent(Event{Key: "k", EventTime: 1001}); !errors.Is(err, ErrBufferLimitExceeded) {
		t.Fatalf("overflow: %v", err)
	}

	if len(j.Results()) != emittedBefore {
		t.Fatalf("emitted changed: before=%d after=%d", emittedBefore, len(j.Results()))
	}
	if bufferedCount(j) != 2 {
		t.Fatalf("buffered changed: want 2, got %d", bufferedCount(j))
	}
	if j.Watermark() != 50 {
		t.Fatalf("watermark changed: %d", j.Watermark())
	}

	// 版本表未被迟到写入污染：最终确定时事件 100 仍只看到 10 号版本。
	out := j.Drain()
	for _, r := range out {
		if r.EventTime == 100 && (r.Kind != KindHit || r.EffectiveAt != 10 || r.Value != "v") {
			t.Fatalf("late write polluted table: %+v", r)
		}
	}
}

// ---- 恰好一次、接受顺序、确定性重放、与朴素查询一致 ----

func TestExactlyOnceAndDeterministicReplay(t *testing.T) {
	scenario := fixedScenario()

	r1 := runScenario(scenario)
	r2 := runScenario(scenario)
	if fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatalf("same input sequence produced different outputs\nrun1=%v\nrun2=%v", r1, r2)
	}

	seen := map[int]bool{}
	for i, r := range r1 {
		if seen[r.Seq] {
			t.Fatalf("seq %d emitted twice", r.Seq)
		}
		seen[r.Seq] = true
		if i > 0 && r1[i-1].Seq > r.Seq {
			t.Fatalf("output not in acceptance order at %d", i)
		}
	}
	if len(seen) != scenario.acceptedEvents {
		t.Fatalf("want %d unique outputs, got %d", scenario.acceptedEvents, len(seen))
	}

	for _, r := range r1 {
		kind, eff, val := scenario.naiveLookup(r.Key, r.EventTime)
		if r.Kind != kind || (kind == KindHit && (r.EffectiveAt != eff || r.Value != val)) {
			t.Fatalf("result %+v != naive kind=%d eff=%d val=%q", r, kind, eff, val)
		}
	}
}

// ---- 并发：版本与事件并发提交，结果恰好一次且与朴素查询一致 ----

func TestConcurrentVersionsAndEvents(t *testing.T) {
	const keyN = 8
	const eventN = 3000

	specs := make([]Version, 0, keyN*7)
	for k := 0; k < keyN; k++ {
		for _, eff := range []int64{1, 5, 10, 50, 100, 500, 1000} {
			v := Version{Key: fmt.Sprintf("k%d", k), EffectiveAt: eff}
			// 固定墓碑：k3 在 10 起为墓碑、k5 在 500 起为墓碑，其余为值。
			if (k == 3 && eff == 10) || (k == 5 && eff == 500) {
				v.Tombstone = true
			} else {
				v.Value = fmt.Sprintf("%s@%d", v.Key, eff)
			}
			specs = append(specs, v)
		}
	}

	// 事件规格也固定下来，两次运行使用完全相同的输入序列。
	rng := rand.New(rand.NewSource(42))
	events := make([]Event, eventN)
	for i := range events {
		events[i] = Event{
			Key:       fmt.Sprintf("k%d", rng.Intn(keyN)),
			EventTime: int64(rng.Intn(1001)),
			Payload:   fmt.Sprintf("p%d", i),
		}
	}

	run := func() []Result {
		j := NewJoiner(0)

		var wg sync.WaitGroup
		for _, v := range specs {
			wg.Add(1)
			go func(v Version) { defer wg.Done(); _ = j.ApplyVersion(v) }(v)
		}
		wg.Wait() // 版本起点均 > 初始水位线，全部接受

		for _, e := range events {
			wg.Add(1)
			go func(e Event) {
				defer wg.Done()
				if st, _, err := j.ProcessEvent(e); err != nil || st != StatusBuffered {
					t.Errorf("event %v rejected unexpectedly: st=%d err=%v", e, st, err)
				}
			}(e)
		}
		wg.Wait()

		// 4 个 goroutine 交错推进水位线（区间有重叠），回退是正常竞争结果。
		var wg2 sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg2.Add(1)
			go func(g int) {
				defer wg2.Done()
				base := int64(g * 250)
				for ts := base + 1; ts <= base+250; ts++ {
					_, _ = j.AdvanceWatermark(ts)
				}
			}(g)
		}
		wg2.Wait()
		_ = j.Drain()
		return j.Results()
	}

	all := run()
	if len(all) != eventN {
		t.Fatalf("want %d outputs, got %d", eventN, len(all))
	}
	seen := map[int]bool{}
	for _, r := range all {
		if seen[r.Seq] {
			t.Fatalf("seq %d emitted twice", r.Seq)
		}
		seen[r.Seq] = true

		kind, eff, val := naiveLookupSpecs(specs, r.Key, r.EventTime)
		if r.Kind != kind || (kind == KindHit && (r.EffectiveAt != eff || r.Value != val)) {
			t.Fatalf("result %+v != naive kind=%d eff=%d val=%q", r, kind, eff, val)
		}
	}

	// 相同输入序列重复计算必须得到完全相同的“连接内容”。
	// 并发下事件的接受顺序（Seq）由调度决定，因此按事件身份（Payload）归一后比对。
	again := run()
	canonical := func(rs []Result) []Result {
		cp := append([]Result(nil), rs...)
		sort.SliceStable(cp, func(i, j int) bool { return cp[i].Payload < cp[j].Payload })
		for i := range cp {
			cp[i].Seq = 0 // Seq 是接受顺序，不参与内容等价
		}
		return cp
	}
	if fmt.Sprint(canonical(all)) != fmt.Sprint(canonical(again)) {
		t.Fatal("concurrent rerun produced different join contents")
	}
}

// ---- 混合并发压力：事件与水位线推进者长期交错，统计恰好一次 ----

func TestConcurrentMixedExactlyOnce(t *testing.T) {
	const producers = 8
	const perP = 200
	j := NewJoiner(0)

	var wg sync.WaitGroup
	var accepted int64
	immediate := sync.Map{} // seq -> struct{}

	for g := 0; g < producers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g + 1)))
			for i := 0; i < perP; i++ {
				key := fmt.Sprintf("k%d", r.Intn(4))
				e := Event{Key: key, EventTime: int64(r.Intn(2000)), Payload: fmt.Sprintf("%d-%d", g, i)}
				st, res, err := j.ProcessEvent(e)
				if err != nil {
					t.Errorf("event rejected: %v", err)
					continue
				}
				atomic.AddInt64(&accepted, 1)
				if st == StatusResolved && res != nil {
					immediate.Store(res.Seq, struct{}{})
				}
			}
		}(g)
	}
	// 水位线缓慢推进，可能与事件提交并发交错。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ts := int64(0); ts <= 1000; ts += 10 {
			_, _ = j.AdvanceWatermark(ts)
		}
	}()
	wg.Wait()
	_ = j.Drain()

	all := j.Results()
	if int64(len(all)) != atomic.LoadInt64(&accepted) {
		t.Fatalf("accepted=%d but emitted=%d", accepted, len(all))
	}
	seen := map[int]bool{}
	for _, r := range all {
		if seen[r.Seq] {
			t.Fatalf("seq %d emitted twice", r.Seq)
		}
		seen[r.Seq] = true
		if r.Kind != KindMiss {
			t.Fatalf("no versions were defined, expected MISS: %+v", r)
		}
	}
	immediate.Range(func(k, _ any) bool {
		if !seen[k.(int)] {
			t.Fatalf("immediately resolved seq %v missing from outputs", k)
		}
		return true
	})
}

// ---- 日志：输入、连接结果、判定依据 ----

func TestLoggingContent(t *testing.T) {
	// nil logger：不输出且不 panic。
	silent := NewJoinerWithLogger(0, nil)
	_ = silent.ApplyVersion(Version{Key: "k", EffectiveAt: 1, Value: "v"})

	var buf bytes.Buffer
	j := NewJoinerWithLogger(0, log.New(&buf, "", 0))
	_ = j.ApplyVersion(Version{Key: "k", EffectiveAt: 1, Value: "v"})
	_, _, _ = j.ProcessEvent(Event{Key: "k", EventTime: 1, Payload: "p"})
	_, _, _ = j.ProcessEvent(Event{Key: "k", EventTime: 9, Payload: "p2"})
	_, _, _ = j.ProcessEvent(Event{Key: "unknown", EventTime: 9, Payload: "p3"})
	_, _ = j.AdvanceWatermark(10)
	_ = j.ApplyVersion(Version{Key: "k", EffectiveAt: 5, Value: "late"})
	_, _, _ = j.ProcessEvent(Event{Key: "", EventTime: 1})
	_, _ = j.AdvanceWatermark(9)

	text := buf.String()
	for _, want := range []string{
		"version{key=k, effectiveAt=1, value=v}", // 输入：版本
		"event{key=k, eventTime=1",               // 输入：事件
		"HIT",                                    // 连接结果
		"MISS",                                   // 连接结果（空键拒绝含 empty key，不含 MISS；这里靠推进后的事件）
		"largest effectiveAt <=",                 // 命中判定依据
		"no version with effectiveAt <=",         // 未命中判定依据
		"reject version", "late",                 // 迟到判定
		"empty key",         // 空键判定
		"regression",        // 回退判定
		"buffered",          // 缓冲判定
		"advance watermark", // 水位线推进
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, text)
		}
	}
}

// ---- 朴素参考模型 ----

type naiveModel struct {
	vers map[string]map[int64]Version // key -> eff -> 最后写入
}

func newNaiveModel() *naiveModel {
	return &naiveModel{vers: map[string]map[int64]Version{}}
}

func (m *naiveModel) put(v Version) {
	if m.vers[v.Key] == nil {
		m.vers[v.Key] = map[int64]Version{}
	}
	m.vers[v.Key][v.EffectiveAt] = v
}

func (m *naiveModel) lookup(key string, t int64) (kind ResultKind, eff int64, val string) {
	return naiveLookup(m.vers, key, t)
}

// naiveLookup 直接在“起点 -> 版本（同点最后写入胜出）”映射上做朴素查询。
func naiveLookup(vers map[string]map[int64]Version, key string, t int64) (kind ResultKind, eff int64, val string) {
	vm := vers[key]
	best, found := int64(0), false
	for e := range vm {
		if e <= t && (!found || e > best) {
			best, found = e, true
		}
	}
	if !found {
		return KindMiss, 0, ""
	}
	if vm[best].Tombstone {
		return KindMiss, 0, ""
	}
	return KindHit, best, vm[best].Value
}

// naiveLookupSpecs 从版本规格列表构造临时朴素视图（同点后者覆盖）。
func naiveLookupSpecs(specs []Version, key string, t int64) (ResultKind, int64, string) {
	vm := map[int64]Version{}
	for _, v := range specs {
		if v.Key == key {
			vm[v.EffectiveAt] = v
		}
	}
	vers := map[string]map[int64]Version{key: vm}
	return naiveLookup(vers, key, t)
}

// ---- 固定场景 ----

type op struct {
	kind int // 0 version, 1 event, 2 watermark
	v    Version
	e    Event
	wm   int64
}

type scenario struct {
	ops            []op
	acceptedEvents int
	model          *naiveModel
}

func fixedScenario() *scenario {
	s := &scenario{model: newNaiveModel()}
	wm := int64(math.MinInt64)
	addV := func(v Version) {
		s.ops = append(s.ops, op{kind: 0, v: v})
		if v.EffectiveAt > wm { // 朴素模型同样拒绝迟到版本
			s.model.put(v)
		}
	}
	addE := func(e Event) {
		s.ops = append(s.ops, op{kind: 1, e: e})
		if e.Key != "" {
			s.acceptedEvents++
		}
	}
	addWM := func(ts int64) {
		s.ops = append(s.ops, op{kind: 2, wm: ts})
		if ts > wm {
			wm = ts
		}
	}

	addV(Version{Key: "a", EffectiveAt: 1, Value: "a1"})
	addV(Version{Key: "a", EffectiveAt: 10, Value: "a2"})
	addV(Version{Key: "a", EffectiveAt: 10, Tombstone: true}) // 同点覆盖为墓碑
	addV(Version{Key: "b", EffectiveAt: 5, Value: "b1"})
	addE(Event{Key: "a", EventTime: 100})
	addE(Event{Key: "b", EventTime: 4})  // 此刻无 b 的版本 => Miss
	addE(Event{Key: "a", EventTime: 10}) // 墓碑 => Miss
	addWM(50)
	addV(Version{Key: "b", EffectiveAt: 40, Value: "b2"})   // 接受
	addV(Version{Key: "b", EffectiveAt: 50, Value: "late"}) // 迟到，朴素模型也不收
	addE(Event{Key: "b", EventTime: 40})                    // 立即确定 => 命中 b2
	addV(Version{Key: "a", EffectiveAt: 60, Value: "a3"})
	addE(Event{Key: "a", EventTime: 60})
	addE(Event{Key: "a", EventTime: 55}) // 仍在墓碑区间 => Miss
	addWM(60)
	addE(Event{Key: "", EventTime: 1})  // 拒绝，不计数
	addWM(59)                           // 回退，拒绝
	addE(Event{Key: "a", EventTime: 1}) // 立即确定 => a1
	return s
}

func (s *scenario) naiveLookup(key string, t int64) (ResultKind, int64, string) {
	return s.model.lookup(key, t)
}

func runScenario(s *scenario) []Result {
	j := NewJoiner(0)
	for _, o := range s.ops {
		switch o.kind {
		case 0:
			_ = j.ApplyVersion(o.v)
		case 1:
			_, _, _ = j.ProcessEvent(o.e)
		case 2:
			_, _ = j.AdvanceWatermark(o.wm)
		}
	}
	_ = j.Drain()
	return j.Results()
}

// ---- 断言辅助 ----

func mustApply(t *testing.T, j *Joiner, v Version) {
	t.Helper()
	if err := j.ApplyVersion(v); err != nil {
		t.Fatalf("apply %+v: %v", v, err)
	}
}

func mustBuffer(t *testing.T, j *Joiner, e Event) {
	t.Helper()
	st, r, err := j.ProcessEvent(e)
	if err != nil || st != StatusBuffered || r != nil {
		t.Fatalf("event %+v: want buffered, got st=%d r=%v err=%v", e, st, r, err)
	}
}

func expectResult(t *testing.T, j *Joiner, e Event, kind ResultKind, eff int64, val string, wantStatus Status) {
	t.Helper()
	st, r, err := j.ProcessEvent(e)
	if err != nil {
		t.Fatalf("event %+v: %v", e, err)
	}
	if st != wantStatus || r == nil {
		t.Fatalf("event %+v status = %d r=%v, want %d with result", e, st, r, wantStatus)
	}
	checkResult(t, *r, kind, eff, val)
}

func checkResult(t *testing.T, r Result, kind ResultKind, eff int64, val string) {
	t.Helper()
	if r.Kind != kind {
		t.Fatalf("kind = %d, want %d (result=%+v)", r.Kind, kind, r)
	}
	if kind == KindHit && (r.EffectiveAt != eff || r.Value != val) {
		t.Fatalf("hit = eff %d val %q, want eff %d val %q", r.EffectiveAt, r.Value, eff, val)
	}
}

func assertResults(t *testing.T, got, want []Result) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d\ngot=%v", len(got), len(want), got)
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Key != w.Key || g.EventTime != w.EventTime ||
			g.EffectiveAt != w.EffectiveAt || g.Value != w.Value || g.Seq != w.Seq {
			t.Fatalf("result[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func bufferedCount(j *Joiner) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.buffered)
}
