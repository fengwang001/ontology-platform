package watermark

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
	"time"
)

func fmtWatermark(v View) (string, string) {
	ew := "-Inf"
	if !v.EventWatermarkNegInf {
		ew = fmt.Sprintf("%d", v.EventWatermark)
	}
	pw := "-Inf"
	if v.ProcessingWatermark != math.MinInt64 {
		pw = fmt.Sprintf("%d", v.ProcessingWatermark)
	}
	return ew, pw
}

// logIngest 打印事件、两个水位、判定与判定依据。
func logIngest(t *testing.T, m *Maintainer, e Event, processingTime int64, decision Decision, basis string) {
	t.Helper()
	v := m.Snapshot()
	ew, pw := fmtWatermark(v)
	t.Logf("事件{id=%s, eventTime=%d, processingTime=%d} 判定=%s 依据=%q | 事件时间水位=%s 处理时间水位=%s 迟到计数=%d",
		e.ID, e.EventTime, processingTime, decision, basis, ew, pw, v.LateCount)
}

func logHeartbeat(t *testing.T, m *Maintainer, processingTime int64, outcome string) {
	t.Helper()
	v := m.Snapshot()
	ew, pw := fmtWatermark(v)
	t.Logf("心跳{processingTime=%d} 结果=%s | 事件时间水位=%s 处理时间水位=%s 迟到计数=%d",
		processingTime, outcome, ew, pw, v.LateCount)
}

func TestRejectionsAreAtomic(t *testing.T) {
	if _, err := New(-1); err == nil {
		t.Fatal("负的允许迟到必须被拒绝")
	} else if re, ok := err.(*RejectError); !ok || re.Reason != RejectNegativeAllowedLateness {
		t.Fatalf("拒绝原因不匹配: %v", err)
	}

	m, _ := New(0)
	m.Ingest(Event{ID: "a", EventTime: 10}, 10)
	m.Heartbeat(5)
	before := m.Snapshot()

	if _, err := m.Ingest(Event{ID: "", EventTime: 1}, 1); err == nil {
		t.Fatal("空标识必须被拒绝")
	} else if re, ok := err.(*RejectError); !ok || re.Reason != RejectEmptyID {
		t.Fatalf("拒绝原因不匹配: %v", err)
	}
	if err := m.Heartbeat(4); err == nil {
		t.Fatal("回退心跳必须被拒绝")
	} else if re, ok := err.(*RejectError); !ok || re.Reason != RejectHeartbeatRegress {
		t.Fatalf("拒绝原因不匹配: %v", err)
	}

	if !viewsEqual(m.Snapshot(), before) {
		t.Fatal("一次失败不得改变两个水位、已接受事件与迟到计数")
	}
}

func TestWatermarkBoundaryAndLateDecision(t *testing.T) {
	// 允许迟到 5：事件时间水位 = maxEvent - 5。
	m, err := New(5)
	if err != nil {
		t.Fatal(err)
	}

	v := m.Snapshot()
	if !v.EventWatermarkNegInf || v.ProcessingWatermark != math.MinInt64 || v.LateCount != 0 {
		t.Fatalf("初始视图异常: %+v", v)
	}
	t.Log("初始 事件时间水位=-Inf 处理时间水位=-Inf 迟到计数=0")

	d, err := m.Ingest(Event{ID: "a", EventTime: 10}, 100)
	if err != nil || d != DecisionAccepted {
		t.Fatalf("首个事件应按时接受: d=%v err=%v", d, err)
	}
	logIngest(t, m, Event{ID: "a", EventTime: 10}, 100, d,
		"未见事件时事件时间水位为负无穷，任何事件时间都按时，水位推进到 10-5=5")

	// 临界：eventTime 恰好等于水位 5，按“不超过即迟到”判迟到。
	d, err = m.Ingest(Event{ID: "b", EventTime: 5}, 999)
	if err != nil || d != DecisionLate {
		t.Fatalf("eventTime==watermark 必须判迟到: d=%v err=%v", d, err)
	}
	logIngest(t, m, Event{ID: "b", EventTime: 5}, 999, d,
		"事件时间 5 不超过事件时间水位 5，判迟到并丢弃，不推进任何水位")

	// 严格大于水位才按时；处理时间落后也不影响。
	d, err = m.Ingest(Event{ID: "c", EventTime: 6}, 2)
	if err != nil || d != DecisionAccepted {
		t.Fatalf("eventTime=水位+1 必须按时: d=%v err=%v", d, err)
	}
	logIngest(t, m, Event{ID: "c", EventTime: 6}, 2, d,
		"事件时间 6 严格大于事件时间水位 5，按时接受；处理时间=2 落后但不参与判定")

	v = m.Snapshot()
	if v.EventWatermark != 5 || v.LateCount != 1 || len(v.AcceptedEvents) != 2 {
		t.Fatalf("maxEvent 仍为 10，水位必须仍为 5：%+v", v)
	}

	d, _ = m.Ingest(Event{ID: "d", EventTime: 20}, 3)
	logIngest(t, m, Event{ID: "d", EventTime: 20}, 3, d,
		"事件时间 20 严格大于水位 5，按时接受，水位随最大事件时间推进到 20-5=15")
	v = m.Snapshot()
	if v.EventWatermark != 15 {
		t.Fatalf("水位必须推进到 15，得到 %d", v.EventWatermark)
	}

	d, _ = m.Ingest(Event{ID: "e", EventTime: 15}, 100000)
	if d != DecisionLate {
		t.Fatal("eventTime==15 必须判迟到")
	}
	logIngest(t, m, Event{ID: "e", EventTime: 15}, 100000, d,
		"事件时间 15 不超过水位 15，判迟到；处理时间 100000 远超事件时间但绝不参与判定")

	v = m.Snapshot()
	if v.LateCount != 2 {
		t.Fatalf("迟到计数应为 2，得到 %d", v.LateCount)
	}
}

func TestProcessingTimeNeverInterferes(t *testing.T) {
	m, _ := New(0)

	if err := m.Heartbeat(1_000_000); err != nil {
		t.Fatal(err)
	}
	logHeartbeat(t, m, 1_000_000, "accepted: 仅推进处理时间水位，不触碰事件时间状态")

	v := m.Snapshot()
	if !v.EventWatermarkNegInf || v.ProcessingWatermark != 1_000_000 {
		t.Fatalf("心跳后事件时间水位必须仍为负无穷: %+v", v)
	}

	d, err := m.Ingest(Event{ID: "x", EventTime: 1}, 999_999)
	if err != nil || d != DecisionAccepted {
		t.Fatalf("处理时间超前不得使事件迟到: d=%v err=%v", d, err)
	}
	logIngest(t, m, Event{ID: "x", EventTime: 1}, 999_999, d,
		"迟到判定只比较事件时间：未见事件时为负无穷，处理时间水位 1000000 不参与")

	v = m.Snapshot()
	if v.ProcessingWatermark != 1_000_000 {
		t.Fatalf("摄入不得改变处理时间水位，得到 %d", v.ProcessingWatermark)
	}

	m.Ingest(Event{ID: "y", EventTime: 10}, 50_000_000)
	d, _ = m.Ingest(Event{ID: "z", EventTime: 1}, 1)
	if d != DecisionLate {
		t.Fatal("事件时间 1 不超过事件时间水位 10，必须迟到")
	}
	logIngest(t, m, Event{ID: "z", EventTime: 1}, 1, d,
		"事件时间 1 不超过事件时间水位 10-0=10，判迟到；处理时间大小不影响")
}

func TestHeartbeatMonotonicAndRejectionAtomic(t *testing.T) {
	m, _ := New(0)
	m.Ingest(Event{ID: "a", EventTime: 100}, 0)

	if err := m.Heartbeat(10); err != nil {
		t.Fatal(err)
	}
	logHeartbeat(t, m, 10, "accepted: 处理时间水位推进到 10")

	if err := m.Heartbeat(10); err != nil {
		t.Fatalf("等值心跳不应被拒绝: %v", err)
	}
	logHeartbeat(t, m, 10, "accepted: 与当前处理时间水位相等，不算回退")

	before := m.Snapshot()
	err := m.Heartbeat(9)
	if err == nil {
		t.Fatal("回退心跳必须被拒绝")
	}
	if re, ok := err.(*RejectError); !ok || re.Reason != RejectHeartbeatRegress {
		t.Fatalf("拒绝原因不匹配: %v", err)
	}
	logHeartbeat(t, m, 9, "rejected(heartbeat_regression): 9 < 当前水位 10")

	if !viewsEqual(m.Snapshot(), before) {
		t.Fatal("回退心跳不得改变两个水位、已接受事件与迟到计数")
	}
}

func TestReplayReproducesState(t *testing.T) {
	m, _ := New(3)
	m.Heartbeat(7)
	m.Ingest(Event{ID: "a", EventTime: 10}, 50)
	m.Ingest(Event{ID: "late1", EventTime: 2}, 60)
	m.Heartbeat(80)
	m.Ingest(Event{ID: "b", EventTime: 20}, 70)
	m.Ingest(Event{ID: "late2", EventTime: 17}, 90)

	if msg := m.SelfCheck(); msg != "" {
		t.Fatalf("自检失败: %s", msg)
	}

	r, err := Replay(m)
	if err != nil {
		t.Fatal(err)
	}
	if !viewsEqual(r.Snapshot(), m.Snapshot()) {
		t.Fatalf("重放视图不一致:\n重放=%+v\n原始=%+v", r.Snapshot(), m.Snapshot())
	}
	t.Logf("按序重放核对一致: 视图=%+v", r.Snapshot())
}

func TestConcurrentIngestHeartbeatAndQuery(t *testing.T) {
	const n = 200
	// 允许迟到覆盖整个事件时间区间：任何乱序交错下，
	// 互不相同的按时事件都必须最终恰好全部出现在视图中。
	m, _ := New(int64(n))

	var wg sync.WaitGroup

	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("e-%03d", i)
			// 处理时间故意与事件时间无关且打乱，证明它不干扰判定。
			processing := int64((i*37 + 11) % (n * 3))
			d, err := m.Ingest(Event{ID: id, EventTime: int64(i)}, processing)
			if err != nil || d != DecisionAccepted {
				t.Errorf("并发摄入 %s 异常: d=%s err=%v", id, d, err)
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for ts := int64(0); ts < 300; ts++ {
			if err := m.Heartbeat(ts); err != nil {
				t.Errorf("心跳 %d 异常: %v", ts, err)
				return
			}
		}
	}()

	stop := make(chan struct{})
	var queryWg sync.WaitGroup
	queryWg.Add(1)
	go func() {
		defer queryWg.Done()
		var lastEvent, lastProc int64 = math.MinInt64, math.MinInt64
		lastEventInf := true
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			v := m.Snapshot()
			if !lastEventInf {
				if v.EventWatermarkNegInf || v.EventWatermark < lastEvent {
					t.Errorf("事件时间水位回退: %d -> %+v", lastEvent, v)
					return
				}
			}
			if v.ProcessingWatermark < lastProc {
				t.Errorf("处理时间水位回退: %d -> %d", lastProc, v.ProcessingWatermark)
				return
			}
			lastEvent, lastEventInf = v.EventWatermark, v.EventWatermarkNegInf
			lastProc = v.ProcessingWatermark
		}
	}()

	wg.Wait()
	close(stop)
	queryWg.Wait()

	v := m.Snapshot()
	if len(v.AcceptedEvents) != n {
		t.Fatalf("并发摄入后必须恰好包含全部 %d 条，得到 %d", n, len(v.AcceptedEvents))
	}
	want := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		want = append(want, fmt.Sprintf("e-%03d", i))
	}
	sort.Strings(want)
	for i := range want {
		if v.AcceptedEvents[i] != want[i] {
			t.Fatalf("视图内容不一致，位置 %d: want %s got %s", i, want[i], v.AcceptedEvents[i])
		}
	}
	if v.LateCount != 0 {
		t.Fatalf("全部事件应按时，迟到计数应为 0，得到 %d", v.LateCount)
	}

	r, err := Replay(m)
	if err != nil {
		t.Fatal(err)
	}
	if !viewsEqual(r.Snapshot(), v) {
		t.Fatalf("并发历史按序重放后必须得到同一终态视图")
	}
	t.Logf("并发 %d 个互不相同按时事件 + 300 次心跳后视图恰好包含全部，两水位单调，重放一致", n)
}
