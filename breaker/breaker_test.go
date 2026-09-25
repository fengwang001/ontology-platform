package breaker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
	"ontology/stat"
)

type mclock struct {
	mu sync.Mutex
	t  time.Time
}

func (m *mclock) Now() time.Time  { m.mu.Lock(); defer m.mu.Unlock(); return m.t }
func (m *mclock) set(t time.Time) { m.mu.Lock(); m.t = t; m.mu.Unlock() }
func (m *mclock) adv(d time.Duration) {
	m.mu.Lock()
	m.t = m.t.Add(d)
	m.mu.Unlock()
}

var boom = errors.New("boom")

func cfg() Config {
	return Config{ConsecutiveFailures: 3, FailureRateThreshold: 0.5, MinSamples: 10,
		Cooldown: 100 * time.Millisecond, MaxCooldown: 250 * time.Millisecond, Probes: 1}
}

func TestValidation(t *testing.T) {
	good := cfg()
	bads := []Config{}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.ConsecutiveFailures = 0 },
		func(c *Config) { c.FailureRateThreshold = 0 },
		func(c *Config) { c.FailureRateThreshold = -0.5 },
		func(c *Config) { c.MinSamples = -1 },
		func(c *Config) { c.Cooldown = 0 },
		func(c *Config) { c.MaxCooldown = c.Cooldown - 1 },
		func(c *Config) { c.Probes = 0 },
	} {
		c := good
		mutate(&c)
		bads = append(bads, c)
	}
	for i, c := range bads {
		if _, err := New(c, nil); err == nil {
			t.Errorf("坏配置 %d 未报错", i)
		}
	}
	if _, err := New(good, nil); err != nil {
		t.Errorf("好配置报错: %v", err)
	}
}

func TestOpenTriggers(t *testing.T) {
	t.Run("consecutive", func(t *testing.T) {
		c := cfg()
		c.MinSamples = 1000 // 关闭失败率条件
		br, _ := New(c, &mclock{t: time.Now()})
		for i := 0; i < 2; i++ {
			br.Report(boom)
		}
		if br.State() != Closed {
			t.Fatal("2 次连续失败不应打开")
		}
		br.Report(boom)
		if br.State() != Open {
			t.Fatal("3 次连续失败应打开")
		}
	})
	t.Run("failure-rate", func(t *testing.T) {
		c := cfg()
		c.ConsecutiveFailures = 1000 // 关闭连续失败条件
		br, _ := New(c, &mclock{t: time.Now()})
		for i := 0; i < 9; i++ {
			if i%2 == 0 {
				br.Report(boom)
			} else {
				br.Report(nil)
			}
		}
		if br.State() != Closed {
			t.Fatal("样本数未达下限不应打开")
		}
		br.Report(nil) // 第 10 个样本，失败率 5/10 = 0.5
		if br.State() != Open {
			t.Fatal("失败率达阈值且样本够应打开")
		}
	})
}

func TestRejectedNotCounted(t *testing.T) {
	c := cfg()
	c.ConsecutiveFailures = 1
	mc := &mclock{t: time.Now()}
	br, _ := New(c, mc)
	br.Report(boom) // 打开
	for i := 0; i < 1000; i++ {
		if err := br.Allow(); !errors.Is(err, ErrOpen) {
			t.Fatalf("打开后应拒绝, got %v", err)
		}
	}
	if br.samples != 1 || br.failures != 1 {
		t.Fatalf("1000 次拒绝改变了统计: samples=%d failures=%d", br.samples, br.failures)
	}
	mc.adv(c.Cooldown)
	if err := br.Allow(); err != nil {
		t.Fatalf("冷却后应半开放行, got %v", err)
	}
	br.Report(nil)
	if br.State() != Closed {
		t.Fatal("半开一次成功应关闭")
	}
}

func TestHalfOpenBackoff(t *testing.T) {
	c := cfg()
	c.ConsecutiveFailures = 1
	mc := &mclock{t: time.Now()}
	br, _ := New(c, mc)
	var seq []time.Duration
	br.Report(boom) // 打开，冷却 100ms
	seq = append(seq, br.cooldown)
	mc.adv(100 * time.Millisecond)
	br.Allow()
	br.Report(boom) // 半开失败 → 重新打开，冷却加倍
	seq = append(seq, br.cooldown)
	mc.adv(100 * time.Millisecond)
	if err := br.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatal("冷却未满应拒绝")
	}
	mc.adv(100 * time.Millisecond)
	br.Allow()
	br.Report(boom) // 再加倍，400ms 封顶 250ms
	seq = append(seq, br.cooldown)
	mc.adv(250 * time.Millisecond)
	br.Allow()
	br.Report(nil) // 半开成功 → 关闭，冷却重置
	seq = append(seq, br.cooldown)
	want := []time.Duration{100, 200, 250, 100}
	for i := range want {
		if seq[i] != want[i]*time.Millisecond {
			t.Fatalf("冷却序列 %v, 期望 %vms", seq, want)
		}
	}
	if br.State() != Closed {
		t.Fatal("半开探测成功应关闭")
	}
	t.Logf("冷却序列: %v", seq)
}

func TestHalfOpenProbeConcurrency(t *testing.T) {
	c := cfg()
	c.ConsecutiveFailures = 1
	c.Probes = 3
	mc := &mclock{t: time.Now()}
	br, _ := New(c, mc)
	br.Report(boom)
	mc.adv(c.Cooldown)
	var passed, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := br.Allow(); err == nil {
				passed.Add(1)
			} else if errors.Is(err, ErrOpen) {
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 3 || rejected.Load() != 97 {
		t.Fatalf("半开探测: 通过 %d 拒绝 %d, 应为 3/97", passed.Load(), rejected.Load())
	}
}

func TestClockBackward(t *testing.T) {
	c := cfg()
	c.ConsecutiveFailures = 1
	mc := &mclock{t: time.Now()}
	br, _ := New(c, mc)
	br.Report(boom)
	mc.set(mc.Now().Add(-time.Second))
	if err := br.Allow(); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("时钟回拨应返回 ErrClockBackward, got %v", err)
	}
	if br.State() != Open || br.transitions != 1 {
		t.Fatal("时钟回拨后状态应保持打开且未迁移")
	}
}

func TestConcurrentMigrationOnce(t *testing.T) {
	c := cfg()
	c.ConsecutiveFailures = 1
	c.Probes = 10
	mc := &mclock{t: time.Now()}
	br, _ := New(c, mc)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); br.Report(boom) }()
	}
	wg.Wait()
	if br.transitions != 1 || br.State() != Open {
		t.Fatalf("并发失败应只迁移一次, transitions=%d", br.transitions)
	}
	mc.adv(c.Cooldown)
	for i := 0; i < 10; i++ {
		br.Allow()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); br.Report(boom) }()
	}
	wg.Wait()
	// 迁移序列：关闭→打开、打开→半开、半开→打开，共 3 次；冷却只加倍一次
	if br.transitions != 3 || br.cooldown != 200*time.Millisecond {
		t.Fatalf("半开并发失败应只重新打开一次且冷却只加倍一次, transitions=%d cooldown=%v",
			br.transitions, br.cooldown)
	}
}

func TestNonRetryableNotCounted(t *testing.T) {
	br, _ := New(cfg(), &mclock{t: time.Now()})
	for i := 0; i < 100; i++ {
		br.Report(fmt.Errorf("bad: %w", classify.ErrNonRetryable))
	}
	if br.State() != Closed || br.samples != 0 {
		t.Fatalf("不可重试错误不应计入熔断, samples=%d", br.samples)
	}
}

func TestRandomStatConsistency(t *testing.T) {
	mc := &mclock{t: time.Now()}
	br, _ := New(Config{ConsecutiveFailures: 5, FailureRateThreshold: 0.5, MinSamples: 20,
		Cooldown: time.Millisecond, MaxCooldown: 10 * time.Millisecond, Probes: 2}, mc)
	bh, _ := bulkhead.New(4, 4)
	st := &stat.Stat{}
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 1000; i++ {
				if r.Intn(10) == 0 {
					mc.adv(time.Millisecond)
				}
				protectedCall(br, bh, st, r.Intn(100))
			}
		}(int64(g))
	}
	wg.Wait()
	s := st.Snapshot()
	if !s.Consistent() {
		t.Errorf("统计等式不成立: %+v", s)
	}
	if s.BreakerRejected == 0 || s.BulkheadRejected == 0 {
		t.Error("随机序列未覆盖熔断/舱壁拒绝路径")
	}
	t.Logf("5 万次随机调用快照: %+v", s)
}

func protectedCall(br *Breaker, bh *bulkhead.Bulkhead, st *stat.Stat, outcome int) {
	if err := br.Allow(); err != nil {
		st.RecordBreakerReject()
		return
	}
	err := bh.Execute(context.Background(), func(context.Context) error {
		switch {
		case outcome < 50:
			return nil
		case outcome < 75:
			return boom
		case outcome < 85:
			return fmt.Errorf("%w", classify.ErrNonRetryable)
		case outcome < 95:
			return fmt.Errorf("%w", classify.ErrTimeout)
		default:
			panic("boom")
		}
	})
	if errors.Is(err, bulkhead.ErrFull) {
		br.Abort()
		st.RecordBulkheadReject()
		return
	}
	if err == nil {
		st.RecordSuccess()
	} else {
		st.RecordFailure(classify.Of(err))
	}
	br.Report(err)
}
