package dedup

import (
	"bytes"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testDeduper(t *testing.T, ttl time.Duration, max int) *Deduper {
	t.Helper()
	d, err := New(Config{
		TTL:        ttl,
		MaxEntries: max,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return d
}

func at(min int, sec ...float64) time.Time {
	base := time.Date(2026, 9, 28, 10, min, 0, 0, time.UTC)
	if len(sec) > 0 {
		base = base.Add(time.Duration(sec[0] * float64(time.Second)))
	}
	return base
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	return re.Reason()
}

// 非法构造参数必须被拒绝。
func TestNew_InvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero ttl", Config{TTL: 0, MaxEntries: 1}},
		{"negative ttl", Config{TTL: -time.Second, MaxEntries: 1}},
		{"zero max entries", Config{TTL: time.Minute, MaxEntries: 0}},
		{"negative max entries", Config{TTL: time.Minute, MaxEntries: -3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := New(tc.cfg)
			if d != nil {
				t.Fatalf("expected nil Deduper, got %+v", d)
			}
			if got := rejectReason(t, err); got != ReasonInvalidConfig {
				t.Fatalf("reason = %q, want %q", got, ReasonInvalidConfig)
			}
		})
	}
}

// 三类拒绝原因必须彼此可区分。
func TestRejectReasons_AreDistinguishable(t *testing.T) {
	_, errCfg := New(Config{TTL: 0, MaxEntries: 0})
	d := testDeduper(t, time.Hour, 1)
	_, errEmpty := d.Process(Event{ID: "", Time: at(0)})
	_, _ = d.Process(Event{ID: "a", Time: at(0)})
	_, errLimit := d.Process(Event{ID: "b", Time: at(1)})

	reasons := []RejectReason{
		rejectReason(t, errCfg),
		rejectReason(t, errEmpty),
		rejectReason(t, errLimit),
	}
	want := []RejectReason{ReasonInvalidConfig, ReasonEmptyID, ReasonMemoryLimit}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
}

// 记忆恰好落在过期边界（watermark == rememberedAt + TTL）时必须立即清除；
// 边界前一纳秒仍算重复。
func TestExpiry_ExactBoundary(t *testing.T) {
	ttl := 5 * time.Minute
	d := testDeduper(t, ttl, 100)

	r := d.mustProcess(t, Event{ID: "a", Time: at(0)})
	if r.Outcome != OutcomeNew || r.MemoryCount != 1 || !r.ExpiresAt.Equal(at(5)) {
		t.Fatalf("a first: %+v", r)
	}

	// 水位线推进到过期点前一纳秒：a 仍在记忆中，判重。
	justBefore := at(5).Add(-time.Nanosecond)
	r = d.mustProcess(t, Event{ID: "x", Time: justBefore})
	if r.MemoryCount != 2 {
		t.Fatalf("before boundary memory count = %d, want 2", r.MemoryCount)
	}
	r = d.mustProcess(t, Event{ID: "a", Time: justBefore})
	if r.Outcome != OutcomeDuplicate {
		t.Fatalf("a one ns before expiry: outcome = %s, want duplicate", r.Outcome)
	}

	// 水位线恰好到达过期点：处理 y 的同一次调用里 a 必须已被清除
	//（结果中记忆条数立即变为 2，而不是等到下一次调用）。
	r = d.mustProcess(t, Event{ID: "y", Time: at(5)})
	if r.MemoryCount != 2 {
		t.Fatalf("at exact boundary memory count = %d, want 2 (a purged, x,y kept)", r.MemoryCount)
	}

	// a 已过期，再次出现应作为全新事件输出。
	r = d.mustProcess(t, Event{ID: "a", Time: at(5)})
	if r.Outcome != OutcomeNew {
		t.Fatalf("a after boundary: outcome = %s, want new", r.Outcome)
	}
	if r.Emitted != 4 { // a, x, y, a
		t.Fatalf("emitted = %d, want 4", r.Emitted)
	}
}

// 重复事件不刷新记忆：过期点始终锚定首现事件时间。
func TestDuplicate_DoesNotRefreshMemory(t *testing.T) {
	d := testDeduper(t, 5*time.Minute, 100)

	r := d.mustProcess(t, Event{ID: "a", Time: at(0)})
	firstExpiry := r.ExpiresAt

	r = d.mustProcess(t, Event{ID: "a", Time: at(2)})
	if r.Outcome != OutcomeDuplicate ||
		!r.RememberedAt.Equal(at(0)) ||
		!r.ExpiresAt.Equal(firstExpiry) {
		t.Fatalf("dup at :02 = %+v, want duplicate anchored at :00", r)
	}
	if r = d.mustProcess(t, Event{ID: "a", Time: at(4)}); r.Outcome != OutcomeDuplicate {
		t.Fatalf("dup at :04 outcome = %s", r.Outcome)
	}

	// 推进水位线到首现过期点，a 被清除。
	if r = d.mustProcess(t, Event{ID: "b", Time: at(5)}); r.MemoryCount != 1 {
		t.Fatalf("after purge memory count = %d, want 1", r.MemoryCount)
	}

	// 若重复曾把记忆刷新到 :02/:04，此刻 a 仍会被判重；实际必须是新事件。
	r = d.mustProcess(t, Event{ID: "a", Time: at(5)})
	if r.Outcome != OutcomeNew || !r.RememberedAt.Equal(at(5)) {
		t.Fatalf("a after original expiry = %+v, want new anchored at :05", r)
	}
}

// 迟到事件不丢弃：照常输出，水位线不回退；并区分记忆能否存活两种情况。
func TestLateEvents_NotDropped(t *testing.T) {
	// 场景一：TTL 足够长，迟到事件被记住，再次出现仍判重。
	d := testDeduper(t, time.Hour, 100)
	_ = d.mustProcess(t, Event{ID: "a", Time: at(0)})
	wm := d.mustProcess(t, Event{ID: "b", Time: at(10)}).Watermark
	if !wm.Equal(at(10)) {
		t.Fatalf("watermark = %v, want :10", wm)
	}

	r := d.mustProcess(t, Event{ID: "c", Time: at(1)})
	if r.Outcome != OutcomeNew || !r.Late || !r.Watermark.Equal(at(10)) {
		t.Fatalf("late new = %+v", r)
	}
	if r = d.mustProcess(t, Event{ID: "c", Time: at(2)}); r.Outcome != OutcomeDuplicate || !r.Late {
		t.Fatalf("late dup = %+v, want duplicate, late", r)
	}

	// 场景二：TTL 较短，迟到新事件的过期点已不晚于水位线：
	// 仍输出，但记忆写入即过期、立即清除，不压制后续同标识事件。
	d2 := testDeduper(t, 5*time.Minute, 100)
	_ = d2.mustProcess(t, Event{ID: "a", Time: at(0)})
	_ = d2.mustProcess(t, Event{ID: "b", Time: at(10)}) // 水位线 :10
	r = d2.mustProcess(t, Event{ID: "z", Time: at(4)})  // 过期点 :09 <= :10
	if r.Outcome != OutcomeNew || !r.Late || r.MemoryCount != 1 {
		t.Fatalf("late new already expired = %+v, want new with count 1", r)
	}
	if r = d2.mustProcess(t, Event{ID: "z", Time: at(4)}); r.Outcome != OutcomeNew {
		t.Fatalf("z repeated after immediate expiry = %s, want new again", r.Outcome)
	}
}

// 水位线单调前进：乱序/相等时间都不允许回退。
func TestWatermark_Monotonic(t *testing.T) {
	d := testDeduper(t, time.Hour, 100)
	seq := []int{5, 9, 9, 3, 12, 12, 1, 12}
	prev := time.Time{}
	for i, m := range seq {
		r := d.mustProcess(t, Event{ID: "x", Time: at(m)})
		if i > 0 && r.Watermark.Before(prev) {
			t.Fatalf("step %d: watermark went backwards %v -> %v", i, prev, r.Watermark)
		}
		prev = r.Watermark
	}
	if !prev.Equal(at(12)) {
		t.Fatalf("final watermark = %v, want :12", prev)
	}
}

// 被拒绝的输入不得改变水位线、记忆、重复计数或已输出事件。
func TestRejection_NoStateMutation(t *testing.T) {
	d := testDeduper(t, time.Hour, 1)
	before := d.mustProcess(t, Event{ID: "a", Time: at(0)})
	snapBefore := d.Snapshot()

	// 空标识：返回零值结果与 empty_id，状态不变。
	r, err := d.Process(Event{ID: "  ", Time: at(1)})
	if reason := rejectReason(t, err); reason != ReasonEmptyID {
		t.Fatalf("reason = %q", reason)
	}
	if r != (Result{}) {
		t.Fatalf("rejected result = %+v, want zero value", r)
	}
	if got := d.Snapshot(); !reflect.DeepEqual(got, snapBefore) {
		t.Fatalf("state changed after empty-id reject:\nbefore %+v\nafter  %+v", snapBefore, got)
	}

	// 容量超限：连试两条不同事件，均拒绝且状态不变，水位线也不推进。
	for _, id := range []string{"b", "c"} {
		_, err = d.Process(Event{ID: id, Time: at(2)})
		if reason := rejectReason(t, err); reason != ReasonMemoryLimit {
			t.Fatalf("id %s reason = %q, want %q", id, reason, ReasonMemoryLimit)
		}
	}
	if got := d.Snapshot(); !reflect.DeepEqual(got, snapBefore) {
		t.Fatalf("state changed after limit rejects:\nbefore %+v\nafter  %+v", snapBefore, got)
	}
	if got := d.Snapshot(); !got.Watermark.Equal(at(0)) {
		t.Fatalf("watermark = %v, want :00", got.Watermark)
	}

	// 被拒绝的 b/c 从未进入记忆：a 仍正常判重，证明没有污染。
	r = d.mustProcess(t, Event{ID: "a", Time: at(3)})
	if r.Outcome != OutcomeDuplicate || r.Duplicates != 1 || r.Emitted != before.Emitted {
		t.Fatalf("a after rejects = %+v", r)
	}
}

// 同一输入序列反复计算必须得到逐字段完全相同的输出（确定性）。
func TestDeterminism_SameSequenceSameOutput(t *testing.T) {
	seq := []Event{
		{ID: "a", Time: at(0)},
		{ID: "b", Time: at(2)},
		{ID: "a", Time: at(1)}, // 乱序，窗口内重复
		{ID: "c", Time: at(6)}, // 推进水位线过 a 的过期点 :05
		{ID: "a", Time: at(6)}, // 过期后再次首现
		{ID: "", Time: at(7)},  // 非法输入
		{ID: "b", Time: at(7)},
		{ID: "d", Time: at(3)}, // 迟到
	}
	run := func() ([]Result, Result) {
		d := testDeduper(t, 5*time.Minute, 10)
		rs := make([]Result, 0, len(seq))
		for _, e := range seq {
			r, err := d.Process(e)
			if err != nil {
				if reason := rejectReason(t, err); reason != ReasonEmptyID {
					t.Fatalf("unexpected reason %q", reason)
				}
				rs = append(rs, Result{Outcome: OutcomeRejected, Reason: ReasonEmptyID})
				continue
			}
			rs = append(rs, r)
		}
		return rs, d.Snapshot()
	}

	rs1, snap1 := run()
	rs2, snap2 := run()
	if !reflect.DeepEqual(rs1, rs2) {
		t.Fatalf("nondeterministic per-event results:\n%+v\n%+v", rs1, rs2)
	}
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("nondeterministic snapshots:\n%+v\n%+v", snap1, snap2)
	}
}

// 并发读写：快照必须字段一致、水位线单调、计数守恒；配合 -race 检测数据竞争。
func TestConcurrent_ReadSnapshotConsistency(t *testing.T) {
	const goroutines, perG = 8, 500
	d := testDeduper(t, 100*time.Hour, goroutines*perG+1)

	var stop atomic.Bool
	var wg sync.WaitGroup

	// 单个读取者连续快照：水位线单调不降，且任意快照内部字段自洽。
	wg.Add(1)
	go func() {
		defer wg.Done()
		prev := time.Time{}
		for !stop.Load() {
			s := d.Snapshot()
			if s.Watermark.Before(prev) {
				t.Errorf("snapshot watermark went backwards: %v -> %v", prev, s.Watermark)
				return
			}
			prev = s.Watermark
			if s.MemoryCount < 0 || s.MemoryCount > goroutines*perG {
				t.Errorf("memory count out of range: %d", s.MemoryCount)
				return
			}
			// 无过期、无重复、无拒绝：记忆数必须恰等于已输出数。
			if int64(s.MemoryCount) != s.Emitted {
				t.Errorf("inconsistent snapshot: count=%d emitted=%d", s.MemoryCount, s.Emitted)
				return
			}
			if s.Duplicates != 0 {
				t.Errorf("unexpected duplicates: %d", s.Duplicates)
				return
			}
		}
	}()

	// 多个写入者各自使用互不相同的标识与单调递增的事件时间。
	var tick atomic.Int64
	var pwg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		pwg.Add(1)
		go func(g int) {
			defer pwg.Done()
			for i := 0; i < perG; i++ {
				m := tick.Add(1)
				r, err := d.Process(Event{
					ID:   eventID(g, i),
					Time: at(0).Add(time.Duration(m) * time.Minute),
				})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if r.Outcome != OutcomeNew {
					t.Errorf("outcome = %s, want new", r.Outcome)
					return
				}
			}
		}(g)
	}
	pwg.Wait()
	stop.Store(true)
	wg.Wait()

	s := d.Snapshot()
	if s.Emitted != goroutines*perG || s.MemoryCount != goroutines*perG {
		t.Fatalf("final snapshot = %+v, want emitted/count = %d", s, goroutines*perG)
	}
}

func eventID(g, i int) string {
	return "g" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
}

// 日志必须包含输入、新/重复判定与判定依据；拒绝时包含原因。
func TestLogging_InputOutcomeAndBasis(t *testing.T) {
	var buf bytes.Buffer
	d, err := New(Config{
		TTL:        5 * time.Minute,
		MaxEntries: 1,
		Logger:     slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = d.Process(Event{ID: "evt-1", Time: at(0)})
	_, _ = d.Process(Event{ID: "evt-1", Time: at(1)})
	_, _ = d.Process(Event{ID: "evt-2", Time: at(2)}) // 容量拒绝
	_, _ = d.Process(Event{ID: "", Time: at(3)})      // 空标识拒绝

	log := buf.String()
	for _, want := range []string{
		"dedup new",
		"dedup duplicate",
		"dedup reject",
		`id=evt-1`,
		"event_time=",
		"watermark=",
		"remembered_at=",
		"expires_at=",
		"basis=",
		"id absent (or expired) at current watermark; first sighting emitted",
		"id remembered and not expired at current watermark; memory not refreshed",
		string(ReasonMemoryLimit),
		string(ReasonEmptyID),
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}

// mustProcess 断言事件未被拒绝并返回结果。
func (d *Deduper) mustProcess(t *testing.T, e Event) Result {
	t.Helper()
	r, err := d.Process(e)
	if err != nil {
		t.Fatalf("Process(%v): unexpected error: %v", e, err)
	}
	return r
}
