package buffer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordSink 记录下游收到的全部批次，可按调用次数注入失败。
// 注入失败时模拟“下游部分应用后出错并自行回滚”，保证本次
// 调用不产生任何部分效果。
type recordSink struct {
	t *testing.T

	mu       sync.Mutex
	received []Entry
	calls    int
	// failCalls 中的调用序号（从 1 开始）会在部分应用后失败并回滚。
	failCalls map[int]bool
}

func newRecordSink(t *testing.T, failCalls ...int) *recordSink {
	s := &recordSink{t: t, failCalls: map[int]bool{}}
	for _, c := range failCalls {
		s.failCalls[c] = true
	}
	return s
}

func (s *recordSink) Flush(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++

	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = fmt.Sprintf("%s#%d", e.Key, e.Seq)
	}
	s.t.Logf("sink flush call=%d entries=[%s]", s.calls, strings.Join(keys, ", "))

	if s.failCalls[s.calls] {
		// 模拟下游部分失败：先应用前半部分，出错后自行回滚。
		applied := entries[:len(entries)/2]
		s.t.Logf("sink 模拟部分失败: 已应用 %d 条后出错, 下游整体回滚", len(applied))
		return fmt.Errorf("downstream partial failure on call %d", s.calls)
	}
	s.received = append(s.received, entries...)
	return nil
}

func (s *recordSink) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.received)
}

func (s *recordSink) entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.received))
	copy(out, s.received)
	return out
}

func newTestBuffer(t *testing.T, cfg Config, sink Sink) *Buffer {
	t.Helper()
	b, err := New(cfg, sink)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func logWrite(t *testing.T, key string, err error, b *Buffer) {
	t.Helper()
	s := b.Stats()
	t.Logf("write key=%q err=%v | buffered=%d accepted=%d flushed=%d rejected=%d clock=%d lastTrigger=%s",
		key, err, s.Buffered, s.Accepted, s.Flushed, s.Rejected, s.Clock, s.LastTrigger)
}

func TestInvalidConfig(t *testing.T) {
	sink := newRecordSink(t)
	cases := []struct {
		name string
		cfg  Config
	}{
		{"batch size 为 0", Config{BatchSize: 0, HighWatermark: 4, MaxDelay: time.Second}},
		{"batch size 为负", Config{BatchSize: -1, HighWatermark: 4, MaxDelay: time.Second}},
		{"高水位小于批量", Config{BatchSize: 4, HighWatermark: 3, MaxDelay: time.Second}},
		{"延迟阈值为 0", Config{BatchSize: 2, HighWatermark: 4, MaxDelay: 0}},
		{"延迟阈值为负", Config{BatchSize: 2, HighWatermark: 4, MaxDelay: -time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg, sink)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err=%v, want ErrInvalidConfig", err)
			}
			t.Logf("判定依据: cfg=%+v -> %v", tc.cfg, err)
		})
	}
	if _, err := New(Config{BatchSize: 2, HighWatermark: 4, MaxDelay: time.Second}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil sink: err=%v, want ErrInvalidConfig", err)
	}
}

func TestEmptyKey(t *testing.T) {
	sink := newRecordSink(t)
	b := newTestBuffer(t, Config{BatchSize: 2, HighWatermark: 4, MaxDelay: time.Hour}, sink)

	err := b.Write(context.Background(), "", "v")
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v, want ErrEmptyKey", err)
	}
	t.Logf("判定依据: 空键整体拒绝 -> %v", err)

	s := b.Stats()
	if s.Accepted != 0 || s.Buffered != 0 || s.Clock != 0 || sink.total() != 0 {
		t.Fatalf("失败改变了状态: %+v, downstream=%d", s, sink.total())
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchTrigger(t *testing.T) {
	sink := newRecordSink(t)
	b := newTestBuffer(t, Config{BatchSize: 3, HighWatermark: 8, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	for _, k := range []string{"a", "b"} {
		logWrite(t, k, b.Write(ctx, k, "v-"+k), b)
	}
	if sink.total() != 0 {
		t.Fatalf("未达到批量阈值不应冲刷, downstream=%d", sink.total())
	}
	t.Logf("判定依据: buffered=2 < batchSize=3, 未触发")

	logWrite(t, "c", b.Write(ctx, "c", "v-c"), b)
	t.Logf("判定依据: buffered 达到 batchSize=3, 触发类型=batch")

	got := sink.entries()
	if len(got) != 3 {
		t.Fatalf("downstream=%d, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Key != want || got[i].Seq != uint64(i+1) {
			t.Fatalf("got[%d]=%s#%d, want %s#%d", i, got[i].Key, got[i].Seq, want, i+1)
		}
	}
	if s := b.Stats(); s.Buffered != 0 || s.Flushed != 3 || s.LastTrigger != TriggerBatch {
		t.Fatalf("stats=%+v", s)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchTriggerPriorityOverDelay(t *testing.T) {
	clock := NewManualClock(time.Now())
	// 第一次冲刷失败，以便从 FlushError 中观察触发类型。
	sink := newRecordSink(t, 1)
	b := newTestBuffer(t, Config{BatchSize: 2, HighWatermark: 8, MaxDelay: time.Second, Clock: clock}, sink)
	ctx := context.Background()

	if err := b.Write(ctx, "a", "v"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second) // 最旧条目已超期，延迟条件成立
	err := b.Write(ctx, "b", "v")  // 同时达到批量阈值

	var fe *FlushError
	if !errors.As(err, &fe) {
		t.Fatalf("err=%v, want FlushError", err)
	}
	t.Logf("判定依据: 批量与延迟条件同时成立, 触发类型=%s (批量优先)", fe.Trigger)
	if fe.Trigger != TriggerBatch {
		t.Fatalf("trigger=%s, want batch", fe.Trigger)
	}
}

func TestDelayTrigger(t *testing.T) {
	clock := NewManualClock(time.Now())
	sink := newRecordSink(t)
	b := newTestBuffer(t, Config{BatchSize: 5, HighWatermark: 8, MaxDelay: 100 * time.Millisecond, Clock: clock}, sink)
	ctx := context.Background()

	logWrite(t, "a", b.Write(ctx, "a", "v-a"), b)
	logWrite(t, "b", b.Write(ctx, "b", "v-b"), b)
	if sink.total() != 0 {
		t.Fatalf("未达到批量阈值不应冲刷, downstream=%d", sink.total())
	}

	clock.Advance(50 * time.Millisecond)
	if err := b.FlushIfDue(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("判定依据: 最旧条目停留 50ms < maxDelay=100ms, 未触发")
	if sink.total() != 0 {
		t.Fatalf("停留时长未达阈值不应冲刷, downstream=%d", sink.total())
	}

	clock.Advance(60 * time.Millisecond)
	if err := b.FlushIfDue(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("判定依据: 最旧条目停留 110ms >= maxDelay=100ms, 触发类型=delay")

	got := sink.entries()
	if len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("downstream=%v, want [a b]", got)
	}
	if s := b.Stats(); s.Buffered != 0 || s.LastTrigger != TriggerDelay {
		t.Fatalf("stats=%+v", s)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestHighWatermarkBackpressure(t *testing.T) {
	// 下游持续失败，使缓冲不被排空，逐步填满到高水位。
	sink := newRecordSink(t, 1, 2, 3)
	b := newTestBuffer(t, Config{BatchSize: 2, HighWatermark: 3, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	logWrite(t, "a", b.Write(ctx, "a", "v-a"), b) // buffered=1
	logWrite(t, "b", b.Write(ctx, "b", "v-b"), b) // buffered=2, 冲刷失败回滚
	logWrite(t, "c", b.Write(ctx, "c", "v-c"), b) // buffered=3, 冲刷失败回滚
	if s := b.Stats(); s.Buffered != 3 {
		t.Fatalf("buffered=%d, want 3", s.Buffered)
	}

	err := b.Write(ctx, "d", "v-d")
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("err=%v, want ErrBackpressure", err)
	}
	t.Logf("判定依据: buffered=3 达到高水位 highWatermark=3, 写入整体拒收 -> %v", err)
	logWrite(t, "d", err, b)

	// 拒收不得改变缓冲内容、顺序、下游已收总量与时钟。
	snap := b.Snapshot()
	if len(snap) != 3 || snap[0].Key != "a" || snap[1].Key != "b" || snap[2].Key != "c" {
		t.Fatalf("buffer=%v", snap)
	}
	s := b.Stats()
	if s.Accepted != 3 || s.Rejected != 1 || s.Clock != 3 || sink.total() != 0 {
		t.Fatalf("stats=%+v downstream=%d", s, sink.total())
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestFlushFailureRollback(t *testing.T) {
	// 第 1 次冲刷下游部分失败并回滚，之后恢复成功。
	sink := newRecordSink(t, 1)
	b := newTestBuffer(t, Config{BatchSize: 2, HighWatermark: 8, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	if err := b.Write(ctx, "a", "v-a"); err != nil {
		t.Fatal(err)
	}
	err := b.Write(ctx, "b", "v-b")

	var fe *FlushError
	if !errors.As(err, &fe) {
		t.Fatalf("err=%v, want FlushError", err)
	}
	t.Logf("判定依据: 下游报错, 整批 %d 条按原顺序放回缓冲头部并立即停止, trigger=%s", fe.Batch, fe.Trigger)

	// 回滚后：缓冲内容与顺序不变、下游已收总量不变、时钟回滚到冲刷前。
	snap := b.Snapshot()
	if len(snap) != 2 || snap[0].Key != "a" || snap[1].Key != "b" ||
		snap[0].Seq != 1 || snap[1].Seq != 2 {
		t.Fatalf("回滚后缓冲=%v", snap)
	}
	s := b.Stats()
	if s.Clock != 2 || s.Accepted != 2 || s.Flushed != 0 || s.FlushFailures != 1 {
		t.Fatalf("回滚后 stats=%+v", s)
	}
	if sink.total() != 0 {
		t.Fatalf("下游已收总量被改变: %d", sink.total())
	}
	t.Logf("回滚校验: buffered=2 clock=%d downstream=%d (均保持冲刷前状态)", s.Clock, sink.total())

	// 下游恢复后，后续写入触发冲刷，条目不丢不重、保持 FIFO。
	logWrite(t, "c", b.Write(ctx, "c", "v-c"), b)
	got := sink.entries()
	if len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("downstream=%v, want [a b]", got)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritesReplay(t *testing.T) {
	clock := NewManualClock(time.Now())
	sink := newRecordSink(t)
	b := newTestBuffer(t, Config{BatchSize: 8, HighWatermark: 64, MaxDelay: time.Hour, Clock: clock}, sink)
	ctx := context.Background()

	const writers = 8
	const perWriter = 50

	var writersWg sync.WaitGroup
	var accepted atomic.Uint64
	for w := 0; w < writers; w++ {
		writersWg.Add(1)
		go func(w int) {
			defer writersWg.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("w%d-%03d", w, i)
				err := b.Write(ctx, key, "v")
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, ErrBackpressure) {
					t.Errorf("write %s: unexpected err %v", key, err)
				}
			}
		}(w)
	}
	// 并发查询与自检。
	stop := make(chan struct{})
	var pollerWg sync.WaitGroup
	pollerWg.Add(1)
	go func() {
		defer pollerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = b.Stats()
				if err := b.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}
	}()
	writersWg.Wait()
	close(stop)
	pollerWg.Wait()

	total := accepted.Load()
	t.Logf("并发写入完成: accepted=%d", total)

	// 推进时钟，用延迟触发把剩余条目排空。
	clock.Advance(2 * time.Hour)
	for b.Stats().Buffered > 0 {
		if err := b.FlushIfDue(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 朴素重放核对：下游收到的条目序号必须恰好是 1..accepted
	// 的连续递增序列（不丢不重、保持接受的 FIFO 顺序）。
	got := sink.entries()
	if uint64(len(got)) != total {
		t.Fatalf("downstream=%d, want accepted=%d", len(got), total)
	}
	for i, e := range got {
		if e.Seq != uint64(i+1) {
			t.Fatalf("重放不一致: got[%d].Seq=%d, want %d", i, e.Seq, i+1)
		}
	}
	t.Logf("朴素重放核对通过: downstream %d 条 == accepted %d 条, 序号 1..%d 连续递增", len(got), total, total)

	s := b.Stats()
	if s.Flushed != total || s.Buffered != 0 {
		t.Fatalf("stats=%+v", s)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
