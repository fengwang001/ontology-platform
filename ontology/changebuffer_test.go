package ontology

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的时钟，保证测试可复现。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recordingSink 记录下游实际收到的条目；failCalls 中编号（从 1 计）的调用
// 返回错误且不产生任何已收条目，模拟“下游部分失败后整体未生效”。
type recordingSink struct {
	t         *testing.T
	mu        sync.Mutex
	received  []Change
	calls     int
	failCalls map[int]error
}

func (s *recordingSink) ApplyBatch(_ context.Context, batch []Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err, ok := s.failCalls[s.calls]; ok {
		s.t.Logf("[sink] 第 %d 次冲刷失败: entries=%v err=%v", s.calls, keysOf(batch), err)
		return err
	}
	s.received = append(s.received, batch...)
	s.t.Logf("[sink] 第 %d 次冲刷成功: entries=%v 下游累计=%d", s.calls, keysOf(batch), len(s.received))
	return nil
}

func (s *recordingSink) receivedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return keysOf(s.received)
}

func keysOf(changes []Change) []string {
	keys := make([]string, len(changes))
	for i, c := range changes {
		keys[i] = c.Key
	}
	return keys
}

func newBuffer(t *testing.T, cfg Config, sink *recordingSink) (*ChangeBuffer, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	cfg.Clock = clock.Now
	buf, err := NewChangeBuffer(cfg, sink)
	if err != nil {
		t.Fatalf("NewChangeBuffer: %v", err)
	}
	return buf, clock
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInvalidConfig(t *testing.T) {
	sink := &recordingSink{t: t}
	cases := []struct {
		name string
		cfg  Config
		sink Sink
	}{
		{"批量大小为 0", Config{BatchSize: 0, HighWater: 4, MaxDelay: time.Second}, sink},
		{"批量大小为负", Config{BatchSize: -1, HighWater: 4, MaxDelay: time.Second}, sink},
		{"高水位小于批量大小", Config{BatchSize: 4, HighWater: 3, MaxDelay: time.Second}, sink},
		{"延迟阈值为 0", Config{BatchSize: 2, HighWater: 4, MaxDelay: 0}, sink},
		{"延迟阈值为负", Config{BatchSize: 2, HighWater: 4, MaxDelay: -time.Second}, sink},
		{"下游为 nil", Config{BatchSize: 2, HighWater: 4, MaxDelay: time.Second}, nil},
	}
	for _, tc := range cases {
		_, err := NewChangeBuffer(tc.cfg, tc.sink)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: 期望 ErrInvalidConfig, 得到 %v", tc.name, err)
		}
		t.Logf("[非法参数] %s -> %v (判定依据: errors.Is(err, ErrInvalidConfig))", tc.name, err)
	}
}

func TestBatchTrigger(t *testing.T) {
	sink := &recordingSink{t: t}
	buf, _ := newBuffer(t, Config{BatchSize: 3, HighWater: 8, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	t.Logf("[写入] k1,k2 (缓冲 2/3, 未达批量, 不触发)")
	if err := buf.Write(ctx, Change{Key: "k1"}, Change{Key: "k2"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := sink.receivedKeys(); len(got) != 0 {
		t.Fatalf("未达批量不应冲刷, 下游已收 %v", got)
	}

	t.Logf("[写入] k3 (缓冲达批量 3, 触发类型=batch)")
	if err := buf.Write(ctx, Change{Key: "k3"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := []string{"k1", "k2", "k3"}
	if got := sink.receivedKeys(); !equalKeys(got, want) {
		t.Fatalf("批量触发冲刷条目不符: got %v want %v", got, want)
	}
	stats := buf.Stats()
	t.Logf("[判定] 触发类型=batch 冲刷条目=%v 统计=%+v", want, stats)
	if stats.Len != 0 || stats.Flushed != 3 {
		t.Fatalf("冲刷后统计异常: %+v", stats)
	}
}

func TestLatencyTrigger(t *testing.T) {
	sink := &recordingSink{t: t}
	buf, clock := newBuffer(t, Config{BatchSize: 4, HighWater: 8, MaxDelay: 100 * time.Millisecond}, sink)
	ctx := context.Background()

	t.Logf("[写入] k1 (缓冲 1/4, 未达批量; 停留 0ms < 100ms, 不触发)")
	if err := buf.Write(ctx, Change{Key: "k1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	clock.Advance(150 * time.Millisecond)
	t.Logf("[自检] 最旧条目停留 150ms >= 阈值 100ms, 期望触发类型=latency")
	trigger, err := buf.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if trigger != TriggerLatency {
		t.Fatalf("触发类型不符: got %v want %v", trigger, TriggerLatency)
	}
	if got := sink.receivedKeys(); !equalKeys(got, []string{"k1"}) {
		t.Fatalf("延迟触发冲刷条目不符: got %v", got)
	}
	t.Logf("[判定] 触发类型=%v 冲刷条目=%v (依据: 最旧条目停留超阈值)", trigger, sink.receivedKeys())
}

func TestBatchTriggerPriority(t *testing.T) {
	sink := &recordingSink{t: t, failCalls: map[int]error{1: errors.New("boom")}}
	buf, clock := newBuffer(t, Config{BatchSize: 2, HighWater: 8, MaxDelay: time.Second}, sink)
	ctx := context.Background()

	t.Logf("[写入] k1,k2 (达批量触发, 下游第 1 次调用失败, 整批回滚留在缓冲)")
	if err := buf.Write(ctx, Change{Key: "k1"}, Change{Key: "k2"}); !errors.Is(err, ErrDownstream) {
		t.Fatalf("期望 ErrDownstream, 得到 %v", err)
	}
	clock.Advance(time.Hour)
	sink.mu.Lock()
	sink.failCalls = nil
	sink.mu.Unlock()
	t.Logf("[自检] 缓冲 2 条同时满足批量(2>=2)与延迟(停留1h>=1s), 批量优先")
	trigger, err := buf.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if trigger != TriggerBatch {
		t.Fatalf("触发类型不符: got %v want %v", trigger, TriggerBatch)
	}
	t.Logf("[判定] 触发类型=%v 冲刷条目=%v (依据: 批量触发优先于延迟触发)", trigger, sink.receivedKeys())
}

func TestHighWaterBackpressure(t *testing.T) {
	sink := &recordingSink{t: t}
	buf, _ := newBuffer(t, Config{BatchSize: 3, HighWater: 4, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	t.Logf("[写入] k1,k2 (缓冲 2/4, 接受)")
	if err := buf.Write(ctx, Change{Key: "k1"}, Change{Key: "k2"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := buf.Snapshot()
	statsBefore := buf.Stats()

	t.Logf("[写入] k3,k4,k5 (2+3>4 超高水位, 期望整体拒收)")
	err := buf.Write(ctx, Change{Key: "k3"}, Change{Key: "k4"}, Change{Key: "k5"})
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("期望 ErrBackpressure, 得到 %v", err)
	}
	t.Logf("[背压] %v (判定依据: errors.Is(err, ErrBackpressure))", err)

	after := buf.Snapshot()
	statsAfter := buf.Stats()
	if !equalKeys(keysOf(after), keysOf(before)) {
		t.Fatalf("拒收后缓冲内容改变: before=%v after=%v", keysOf(before), keysOf(after))
	}
	if statsAfter.Accepted != statsBefore.Accepted || statsAfter.Rejected != 3 {
		t.Fatalf("拒收后统计异常: %+v", statsAfter)
	}
	if got := sink.receivedKeys(); len(got) != 0 {
		t.Fatalf("拒收不应影响下游: %v", got)
	}
	t.Logf("[判定] 缓冲保持=%v 下游已收=0 统计=%+v (依据: 整体拒收不入缓冲)", keysOf(after), statsAfter)
}

func TestEmptyKeyRejected(t *testing.T) {
	sink := &recordingSink{t: t}
	buf, _ := newBuffer(t, Config{BatchSize: 3, HighWater: 8, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	if err := buf.Write(ctx, Change{Key: "k1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := buf.Snapshot()

	t.Logf("[写入] k2,\",k4 (含空键, 期望整批拒绝)")
	err := buf.Write(ctx, Change{Key: "k2"}, Change{Key: ""}, Change{Key: "k4"})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("期望 ErrEmptyKey, 得到 %v", err)
	}
	t.Logf("[空键] %v (判定依据: errors.Is(err, ErrEmptyKey))", err)
	if after := buf.Snapshot(); !equalKeys(keysOf(after), keysOf(before)) {
		t.Fatalf("空键拒绝后缓冲改变: before=%v after=%v", keysOf(before), keysOf(after))
	}
	t.Logf("[判定] 缓冲保持=%v (依据: 整批拒绝, 一条不入)", keysOf(buf.Snapshot()))
}

func TestDownstreamFailureRollback(t *testing.T) {
	sink := &recordingSink{t: t, failCalls: map[int]error{1: errors.New("下游第 2 条写崩, 整体未生效")}}
	buf, clock := newBuffer(t, Config{BatchSize: 3, HighWater: 8, MaxDelay: 100 * time.Millisecond}, sink)
	ctx := context.Background()

	t.Logf("[写入] k1 (缓冲 1/3, 未达批量, 不触发)")
	if err := buf.Write(ctx, Change{Key: "k1"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	clock.Advance(100 * time.Millisecond)
	t.Logf("[自检] 停留 100ms 达阈值, 延迟触发; 下游报错, 期望整批回滚")
	trigger, err := buf.Check(ctx)
	if trigger != TriggerLatency {
		t.Fatalf("触发类型不符: got %v want %v", trigger, TriggerLatency)
	}
	if !errors.Is(err, ErrDownstream) {
		t.Fatalf("期望 ErrDownstream, 得到 %v", err)
	}
	t.Logf("[回滚] %v (判定依据: errors.Is(err, ErrDownstream))", err)

	snap := buf.Snapshot()
	stats := buf.Stats()
	if !equalKeys(keysOf(snap), []string{"k1"}) {
		t.Fatalf("回滚后缓冲顺序不符: %v", keysOf(snap))
	}
	if stats.Flushed != 0 || stats.Rolled != 1 {
		t.Fatalf("回滚后统计异常: %+v", stats)
	}
	if got := sink.receivedKeys(); len(got) != 0 {
		t.Fatalf("下游已收总量不应改变: %v", got)
	}
	t.Logf("[判定] 缓冲按原顺序放回=%v 下游已收=0 统计=%+v", keysOf(snap), stats)

	// 时钟回滚验证: oldestAt 恢复为冲刷前(T0)。只再推进 50ms,
	// 若时钟正确回滚, 停留时长为 150ms >= 100ms, 延迟触发重放;
	// 若被错误重置为冲刷时刻, 停留仅 50ms, 不会触发。
	sink.mu.Lock()
	sink.failCalls = nil
	sink.mu.Unlock()
	clock.Advance(50 * time.Millisecond)
	t.Logf("[自检] 仅再推进 50ms, oldestAt 已回滚到冲刷前, 累计停留 150ms, 延迟触发重放")
	trigger, err = buf.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if trigger != TriggerLatency {
		t.Fatalf("触发类型不符: got %v want %v", trigger, TriggerLatency)
	}
	if got := sink.receivedKeys(); !equalKeys(got, []string{"k1"}) {
		t.Fatalf("重放后下游条目不符: %v", got)
	}
	t.Logf("[判定] 重放条目=%v (依据: 回滚不丢不重, 时钟恢复后可复现重放)", sink.receivedKeys())
}

// TestConcurrentWritesFIFO 并发写入后冲刷排空, 用朴素重放核对:
// 下游收到的条目必须恰好是被接受写入的 FIFO 顺序, 不丢不重。
func TestConcurrentWritesFIFO(t *testing.T) {
	sink := &recordingSink{t: t}
	buf, clock := newBuffer(t, Config{BatchSize: 4, HighWater: 256, MaxDelay: time.Hour}, sink)
	ctx := context.Background()

	const writers = 8
	const perWriter = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("w%02d-k%02d", id, i)
				if err := buf.Write(ctx, Change{Key: key}); err != nil {
					t.Errorf("write %s: %v", key, err)
				}
			}
		}(w)
	}
	// 并发执行查询与自检。
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = buf.Stats()
				_ = buf.Snapshot()
			}
		}
	}()
	wg.Wait()
	close(done)

	// 推进时钟冲刷剩余不足一批的条目。
	clock.Advance(time.Hour)
	if _, err := buf.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}

	stats := buf.Stats()
	got := sink.receivedKeys()
	t.Logf("[并发] 写入=%d 接受=%d 拒绝=%d 冲刷=%d 回滚=%d 下游已收=%d",
		writers*perWriter, stats.Accepted, stats.Rejected, stats.Flushed, stats.Rolled, len(got))

	if stats.Accepted != writers*perWriter || stats.Rejected != 0 {
		t.Fatalf("高水位足够, 不应有拒收: %+v", stats)
	}
	if stats.Flushed != stats.Accepted || stats.Len != 0 {
		t.Fatalf("排空后统计异常: %+v", stats)
	}
	if uint64(len(got)) != stats.Accepted {
		t.Fatalf("下游已收 %d != 接受 %d (丢/重)", len(got), stats.Accepted)
	}

	// 不丢: 每个键恰好出现一次。
	seen := make(map[string]int, len(got))
	for _, k := range got {
		seen[k]++
	}
	if len(seen) != len(got) {
		t.Fatalf("下游收到重复条目")
	}
	// 不重且 FIFO: 同一写入者的键在下游保持其发出顺序。
	next := make([]int, writers)
	for _, k := range got {
		var wid, seq int
		if _, err := fmt.Sscanf(k, "w%02d-k%02d", &wid, &seq); err != nil {
			t.Fatalf("非法键 %q", k)
		}
		if seq != next[wid] {
			t.Fatalf("写入者 %d 的 FIFO 顺序被破坏: 期望序号 %d, 得到 %d (键 %s)", wid, next[wid], seq, k)
		}
		next[wid]++
	}
	t.Logf("[判定] 下游条目数=%d 与接受写入一致, 每个键恰好一次, 各写入者内部 FIFO 有序 (依据: 朴素重放核对不丢不重)", len(got))
}

// TestNaiveReplay 朴素重放核对: 用一段脚本化操作模拟混合场景,
// 维护一个朴素模型(接受的写入按 FIFO 追加), 最终下游收到的序列
// 必须等于朴素模型重放结果。
func TestNaiveReplay(t *testing.T) {
	sink := &recordingSink{t: t, failCalls: map[int]error{2: errors.New("transient")}}
	buf, clock := newBuffer(t, Config{BatchSize: 3, HighWater: 5, MaxDelay: time.Minute}, sink)
	ctx := context.Background()

	var model []string
	step := func(op string, err error) {
		t.Logf("[重放] %s -> err=%v 模型长度=%d 缓冲=%v", op, err, len(model), keysOf(buf.Snapshot()))
	}

	// 1. 写 3 条 -> 批量触发, 第 1 次冲刷成功。
	if err := buf.Write(ctx, Change{Key: "a"}, Change{Key: "b"}, Change{Key: "c"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	model = append(model, "a", "b", "c")
	step("write a,b,c (批量触发, 成功)", nil)

	// 2. 写 3 条 -> 批量触发, 第 2 次冲刷失败, 整批回滚; 模型不变。
	err := buf.Write(ctx, Change{Key: "d"}, Change{Key: "e"}, Change{Key: "f"})
	if !errors.Is(err, ErrDownstream) {
		t.Fatalf("期望 ErrDownstream, 得到 %v", err)
	}
	model = append(model, "d", "e", "f")
	step("write d,e,f (批量触发, 下游失败回滚)", err)

	// 3. 缓冲 3 条, 再写 3 条超高水位 -> 整体拒收; 模型不变。
	err = buf.Write(ctx, Change{Key: "x"}, Change{Key: "y"}, Change{Key: "z"})
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("期望 ErrBackpressure, 得到 %v", err)
	}
	step("write x,y,z (高水位拒收)", err)

	// 4. 下游恢复, 推进时钟 -> 延迟触发, 重放 d,e,f。
	clock.Advance(time.Minute)
	if _, err := buf.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	step("check (延迟触发, 重放回滚批)", nil)

	// 5. 再写 2 条 -> 未达批量; 推进时钟 -> 延迟触发冲刷。
	if err := buf.Write(ctx, Change{Key: "g"}, Change{Key: "h"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	model = append(model, "g", "h")
	clock.Advance(time.Minute)
	if _, err := buf.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	step("write g,h + check (延迟触发)", nil)

	got := sink.receivedKeys()
	if !equalKeys(got, model) {
		t.Fatalf("朴素重放核对失败:\n  下游收到: %s\n  模型重放: %s", strings.Join(got, ","), strings.Join(model, ","))
	}
	stats := buf.Stats()
	t.Logf("[判定] 下游=%v 模型=%v 统计=%+v (依据: 下游序列 == 接受写入的 FIFO 朴素重放)", got, model, stats)
}
