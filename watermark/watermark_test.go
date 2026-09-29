package watermark

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

var testBase = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return testBase.Add(time.Duration(min) * time.Minute) }

// logDecision 按需求打印事件、两个水位、判定与判定依据。
func logDecision(t *testing.T, r IngestResult) {
	t.Helper()
	ew := "-infinity"
	if r.EventWatermarkSet {
		ew = r.EventWatermark.Format(time.RFC3339Nano)
	}
	pw := "-infinity"
	if r.ProcessingSet {
		pw = r.ProcessingWatermark.Format(time.RFC3339Nano)
	}
	t.Logf("event{id=%s time=%s} event_watermark=%s processing_watermark=%s decision=%s basis=%q",
		r.EventID, r.EventTime.Format(time.RFC3339Nano), ew, pw, r.Decision, r.Basis)
}

func rejectReason(t *testing.T, err error) Reason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RejectError, got %v", err)
	}
	return re.Reason
}

func TestNegativeAllowedLatenessRejected(t *testing.T) {
	_, err := New(-time.Nanosecond)
	if err == nil {
		t.Fatal("negative allowed lateness must be rejected")
	}
	if got := rejectReason(t, err); got != ReasonNegativeAllowedLateness {
		t.Fatalf("reason = %s, want %s", got, ReasonNegativeAllowedLateness)
	}
	if m, err := New(0); err != nil || m == nil {
		t.Fatalf("zero allowed lateness must be allowed, got m=%v err=%v", m, err)
	}
}

func TestFirstEventAndWatermarkInit(t *testing.T) {
	m, err := New(2 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if v := m.View(); v.EventWatermarkSet {
		t.Fatal("event watermark must be -infinity before any event")
	}
	r, err := m.Ingest("e1", at(10))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, r)
	if r.Decision != DecisionAccepted || !r.EventWatermark.Equal(at(8)) {
		t.Fatalf("first event accepted with watermark 10:08, got decision=%s wm=%s",
			r.Decision, r.EventWatermark)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestEventAtWatermarkIsExactlyLate(t *testing.T) {
	m, _ := New(2 * time.Minute)
	r1, _ := m.Ingest("e1", at(10))
	logDecision(t, r1)

	// 恰好等于水位 10:08：“不超过” => 迟到。
	rBoundary, err := m.Ingest("e-boundary", at(8))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, rBoundary)
	if rBoundary.Decision != DecisionLate {
		t.Fatalf("event time equal to watermark must be late, got %s", rBoundary.Decision)
	}

	// 严格大于水位 10:08（晚 1 纳秒）=> 按时。
	rJustAfter, err := m.Ingest("e-just-after", at(8).Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, rJustAfter)
	if rJustAfter.Decision != DecisionAccepted {
		t.Fatalf("event time just after watermark must be accepted, got %s", rJustAfter.Decision)
	}

	v := m.View()
	if v.AcceptedCount != 2 || v.LateCount != 1 {
		t.Fatalf("counts = accepted %d late %d, want 2 and 1", v.AcceptedCount, v.LateCount)
	}
	if v.Accepted[0].ID != "e1" || v.Accepted[1].ID != "e-just-after" {
		t.Fatalf("accepted order = %v", v.Accepted)
	}
	if !v.EventWatermark.Equal(at(8)) {
		t.Fatalf("late event must not move watermark: %s, want 10:08", v.EventWatermark)
	}
}

func TestLateEventChangesNoStateButCounter(t *testing.T) {
	m, _ := New(time.Minute)
	m.Ingest("e1", at(10))
	before := m.View()

	r, err := m.Ingest("late1", at(5))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, r)
	after := m.View()
	if r.Decision != DecisionLate || after.LateCount != 1 {
		t.Fatalf("expected one late event, got decision=%s count=%d", r.Decision, after.LateCount)
	}
	if !after.EventWatermark.Equal(before.EventWatermark) ||
		after.AcceptedCount != before.AcceptedCount {
		t.Fatal("late event must not move event watermark or accepted view")
	}
	r2, _ := m.Ingest("late2", at(9))
	logDecision(t, r2)
	if m.View().LateCount != 2 {
		t.Fatal("second late event must raise late count to 2")
	}
}

func TestProcessingTimeFarAheadDoesNotAffectDecision(t *testing.T) {
	m, _ := New(time.Minute)
	future := at(10).Add(365 * 24 * time.Hour)
	if err := m.Heartbeat(future); err != nil {
		t.Fatal(err)
	}

	// 事件时间 10:05 对事件时间水位（负无穷）必须按时，尽管处理时间已在一年后。
	r, err := m.Ingest("e1", at(5))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, r)
	if r.Decision != DecisionAccepted {
		t.Fatalf("processing watermark far ahead must not decide lateness, got %s", r.Decision)
	}
	// 事件水位 10:04；处理时间仍在一年后，10:04:30 严格大于事件水位，
	// 必须按事件时间判按时——与超前的处理时间完全无关。
	r2, err := m.Ingest("e2", at(4).Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, r2)
	if r2.Decision != DecisionAccepted {
		t.Fatalf("event 10:04:30 must be accepted against event watermark 10:04, got %s", r2.Decision)
	}
	// 10:04:00 恰好等于水位，仍按事件时间判迟到，即便处理时间在一年后。
	r3, err := m.Ingest("e3", at(4))
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, r3)
	if r3.Decision != DecisionLate {
		t.Fatalf("event 10:04:00 equal to event watermark must be late, got %s", r3.Decision)
	}
	if v := m.View(); !v.ProcessingWatermark.Equal(future) {
		t.Fatalf("processing watermark = %s, want %s", v.ProcessingWatermark, future)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatDoesNotTouchEventState(t *testing.T) {
	m, _ := New(time.Minute)
	m.Ingest("e1", at(10))
	before := m.View()

	if err := m.Heartbeat(at(11)); err != nil {
		t.Fatal(err)
	}
	after := m.View()
	if !after.EventWatermark.Equal(before.EventWatermark) ||
		after.AcceptedCount != before.AcceptedCount ||
		after.LateCount != before.LateCount {
		t.Fatal("heartbeat must not change any event-time state")
	}
	if !after.ProcessingWatermark.Equal(at(11)) {
		t.Fatalf("processing watermark = %s, want 11:00", after.ProcessingWatermark)
	}
}

func TestHeartbeatMonotonicAndRejectedRegression(t *testing.T) {
	m, _ := New(0)
	if err := m.Heartbeat(at(10)); err != nil {
		t.Fatal(err)
	}
	// 相等的心跳允许（非回退）。
	if err := m.Heartbeat(at(10)); err != nil {
		t.Fatalf("equal heartbeat must be allowed, got %v", err)
	}
	if err := m.Heartbeat(at(11)); err != nil {
		t.Fatal(err)
	}

	before := m.View()
	err := m.Heartbeat(at(11).Add(-time.Nanosecond))
	if err == nil {
		t.Fatal("regressing heartbeat must be rejected")
	}
	if got := rejectReason(t, err); got != ReasonHeartbeatRegression {
		t.Fatalf("reason = %s, want %s", got, ReasonHeartbeatRegression)
	}
	if after := m.View(); !after.ProcessingWatermark.Equal(before.ProcessingWatermark) {
		t.Fatal("rejected heartbeat must not move processing watermark")
	}
}

func TestEmptyIDRejectedWithoutStateChange(t *testing.T) {
	m, _ := New(time.Minute)
	m.Ingest("e1", at(10))
	m.Heartbeat(at(10))
	before := m.View()

	_, err := m.Ingest("", at(20))
	if err == nil {
		t.Fatal("empty id must be rejected")
	}
	if got := rejectReason(t, err); got != ReasonEmptyID {
		t.Fatalf("reason = %s, want %s", got, ReasonEmptyID)
	}
	after := m.View()
	if after.AcceptedCount != before.AcceptedCount ||
		after.LateCount != before.LateCount ||
		!after.EventWatermark.Equal(before.EventWatermark) ||
		!after.ProcessingWatermark.Equal(before.ProcessingWatermark) {
		t.Fatal("rejected ingest must not change either watermark or counters")
	}
}

func TestOutOfOrderEventsAdvanceMonotonically(t *testing.T) {
	m, _ := New(0)
	seq := []struct {
		id string
		t  time.Time
		d  Decision
	}{
		{"a", at(10), DecisionAccepted},
		{"b", at(12), DecisionAccepted},
		{"c", at(12), DecisionLate}, // 恰好等于水位（允许迟到为 0）
		{"d", at(11), DecisionLate},
		{"e", at(13), DecisionAccepted},
	}
	var lastWM time.Time
	haveWM := false
	for _, s := range seq {
		r, err := m.Ingest(s.id, s.t)
		if err != nil {
			t.Fatal(err)
		}
		logDecision(t, r)
		if r.Decision != s.d {
			t.Fatalf("%s: decision = %s, want %s", s.id, r.Decision, s.d)
		}
		if r.Decision == DecisionAccepted {
			if haveWM && r.EventWatermark.Before(lastWM) {
				t.Fatal("event watermark must be monotonic non-decreasing")
			}
			lastWM, haveWM = r.EventWatermark, true
		}
	}
	v := m.View()
	if v.AcceptedCount != 3 || v.LateCount != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", v.AcceptedCount, v.LateCount)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentDistinctOnTimeEventsAllVisible(t *testing.T) {
	const n = 200
	// 允许迟到（10m）严格大于事件时间跨度（199s），
	// 因此即使最大时间戳最先被调度，全部事件仍严格大于水位、必然按时。
	m, _ := New(10 * time.Minute)

	// 事件时间严格递增且互不相同：无论 goroutine 调度顺序如何，全部按时。
	var wg sync.WaitGroup
	var failMu sync.Mutex
	var failures []error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("evt-%03d", i)
			r, err := m.Ingest(id, at(10).Add(time.Duration(i)*time.Second))
			if err != nil {
				failMu.Lock()
				failures = append(failures, err)
				failMu.Unlock()
				return
			}
			if r.Decision != DecisionAccepted {
				failMu.Lock()
				failures = append(failures, fmt.Errorf("%s decided %s", id, r.Decision))
				failMu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	for _, err := range failures {
		t.Error(err)
	}

	v := m.View()
	if v.AcceptedCount != n || v.LateCount != 0 {
		t.Fatalf("counts = %d/%d, want %d/0", v.AcceptedCount, v.LateCount, n)
	}
	seen := make(map[string]bool, n)
	for _, ev := range v.Accepted {
		if seen[ev.ID] {
			t.Fatalf("duplicate accepted event %s", ev.ID)
		}
		seen[ev.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("view contains %d distinct events, want %d", len(seen), n)
	}
	wantWM := at(10).Add(199*time.Second - 10*time.Minute)
	if !v.EventWatermark.Equal(wantWM) {
		t.Fatalf("event watermark = %s, want %s", v.EventWatermark, wantWM)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentIngestHeartbeatAndReaders(t *testing.T) {
	// 跨度 99s，允许迟到取 5m，保证任意调度顺序下全部按时。
	m, _ := New(5 * time.Minute)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var workers sync.WaitGroup

	// 查询与自检并发读，必须始终观察到一致状态。
	for k := 0; k < 4; k++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					v := m.View()
					if v.AcceptedCount < 0 || v.LateCount < 0 {
						t.Error("negative counts observed")
						return
					}
					if err := m.CheckInvariants(); err != nil {
						t.Errorf("invariant violated: %v", err)
						return
					}
					runtime.Gosched()
				}
			}
		}()
	}

	// 心跳严格递增推进处理水位。
	for i := 0; i < 50; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			_ = m.Heartbeat(at(10).Add(time.Duration(i) * time.Second))
		}(i)
	}

	// 摄入互不相同的按时事件。
	for i := 0; i < 100; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			_, _ = m.Ingest(fmt.Sprintf("c-%03d", i), at(20).Add(time.Duration(i)*time.Second))
		}(i)
	}

	workers.Wait()
	close(stop)
	readers.Wait()

	v := m.View()
	if v.AcceptedCount != 100 || v.LateCount != 0 {
		t.Fatalf("counts = %d/%d, want 100/0", v.AcceptedCount, v.LateCount)
	}
	if !v.ProcessingSet || !v.ProcessingWatermark.Equal(at(10).Add(49*time.Second)) {
		t.Fatalf("processing watermark = %v (%v), want 10:00:49",
			v.ProcessingWatermark, v.ProcessingSet)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// recordedEvent 是一次按序重放所需的最小输入。
type recordedEvent struct {
	id string
	t  time.Time
}

func replay(allowed time.Duration, events []recordedEvent) (accepted, late int, finalWM time.Time, set bool) {
	m, err := New(allowed)
	if err != nil {
		panic(err)
	}
	for _, ev := range events {
		r, err := m.Ingest(ev.id, ev.t)
		if err != nil {
			panic(err)
		}
		set = r.EventWatermarkSet
	}
	v := m.View()
	return v.AcceptedCount, v.LateCount, v.EventWatermark, set
}

func TestDeterministicOrderedReplay(t *testing.T) {
	// 同一份事件日志，按相同顺序重放两次（心跳被刻意忽略），结果必须完全一致；
	// 且“乱序穿插心跳”与“完全不穿插心跳”的事件侧结果相同。
	log := []recordedEvent{
		{"a", at(10)}, {"b", at(12)}, {"c", at(11)},
		{"d", at(12)}, {"e", at(15)}, {"f", at(11)},
	}

	a1, l1, wm1, set1 := replay(2*time.Minute, log)
	a2, l2, wm2, set2 := replay(2*time.Minute, log)
	if a1 != a2 || l1 != l2 || !wm1.Equal(wm2) || set1 != set2 {
		t.Fatalf("replays differ: (%d,%d,%s,%v) vs (%d,%d,%s,%v)",
			a1, l1, wm1, set1, a2, l2, wm2, set2)
	}

	// 带任意心跳的重放：事件侧结果与无心跳重放一致。
	m, _ := New(2 * time.Minute)
	_ = m.Heartbeat(at(30))
	for i, ev := range log {
		if i == 2 {
			_ = m.Heartbeat(at(40))
		}
		r, err := m.Ingest(ev.id, ev.t)
		if err != nil {
			t.Fatal(err)
		}
		logDecision(t, r)
	}
	v := m.View()
	if v.AcceptedCount != a1 || v.LateCount != l1 {
		t.Fatalf("event-side result with heartbeats = %d/%d, want %d/%d",
			v.AcceptedCount, v.LateCount, a1, l1)
	}
	if !v.EventWatermark.Equal(wm1) {
		t.Fatalf("event watermark with heartbeats = %s, want %s", v.EventWatermark, wm1)
	}
	if !v.ProcessingWatermark.Equal(at(40)) {
		t.Fatalf("processing watermark should still track heartbeats: %s", v.ProcessingWatermark)
	}
}
