package watermark

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// captureLogger 收集日志输出，供断言输入、合并水位与判定依据是否被打印。
type captureLogger struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *captureLogger) Printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(&c.buf, format+"\n", args...)
}

func (c *captureLogger) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func newTestMerger(t *testing.T, partitions int, threshold Time) *Merger {
	t.Helper()
	m, err := New(Config{Partitions: partitions, IdleThreshold: threshold})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return m
}

// TestBasicMerge 验证合并水位取非空闲分区水位的最小值。
func TestBasicMerge(t *testing.T) {
	m := newTestMerger(t, 3, 100)

	if got := m.Merged(); got != 0 {
		t.Fatalf("initial merged = %d, want 0", got)
	}

	got, err := m.Report(0, 0, 10)
	if err != nil || got != 10 {
		t.Fatalf("report p0=10: got %d, err %v", got, err)
	}
	got, err = m.Report(1, 0, 30)
	if err != nil || got != 10 {
		t.Fatalf("report p1=30: merged = %d, err %v, want 10 (min)", got, err)
	}
	got, err = m.Report(2, 0, 20)
	if err != nil || got != 10 {
		t.Fatalf("report p2=20: merged = %d, err %v, want 10 (min)", got, err)
	}

	// 最慢分区 p0 推进到 25，合并水位随之推进到 min(25,30,20)=20。
	got, err = m.Report(0, 1, 25)
	if err != nil || got != 20 {
		t.Fatalf("report p0=25: merged = %d, err %v, want 20", got, err)
	}
}

// TestIdleBoundaryExactEqual 验证空闲边界：elapsed == threshold 恰好相等时空闲。
func TestIdleBoundaryExactEqual(t *testing.T) {
	m := newTestMerger(t, 1, 10)
	if _, err := m.Report(0, 0, 100); err != nil {
		t.Fatalf("report: %v", err)
	}

	// elapsed=9 < 10：仍活跃，候选存在。
	if _, err := m.AdvanceClock(9); err != nil {
		t.Fatalf("advance 9: %v", err)
	}
	if snap := m.Snapshot(); snap.Partitions[0].Idle {
		t.Fatalf("at t=9 partition unexpectedly idle: %+v", snap.Partitions[0])
	}
	if got := m.Merged(); got != 100 {
		t.Fatalf("at t=9 merged = %d, want 100", got)
	}

	// elapsed=10 == threshold：恰好空闲，全部空闲 -> 合并水位保持不变。
	if _, err := m.AdvanceClock(10); err != nil {
		t.Fatalf("advance 10: %v", err)
	}
	snap := m.Snapshot()
	if !snap.Partitions[0].Idle {
		t.Fatalf("at t=10 partition should be idle: %+v", snap.Partitions[0])
	}
	if got := m.Merged(); got != 100 {
		t.Fatalf("at t=10 merged = %d, want 100 held", got)
	}
}

// TestIdlePartitionDoesNotBlockMerge 验证长时间无数据的分区不会卡死整体水位。
func TestIdlePartitionDoesNotBlockMerge(t *testing.T) {
	m := newTestMerger(t, 2, 10)
	if _, err := m.Report(0, 0, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Report(1, 0, 100); err != nil {
		t.Fatal(err)
	}

	// p1 持续推进；p0 在 t=10 起空闲，不再拖低/拖住合并水位。
	if _, err := m.Report(1, 5, 200); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.AdvanceClock(10); got != 200 {
		t.Fatalf("merged at t=10 = %d, want 200 (p0 idle, p1 active)", got)
	}
	if got, _ := m.AdvanceClock(50); got != 200 {
		t.Fatalf("merged at t=50 = %d, want 200 held (all idle)", got)
	}
}

// TestAllIdleHoldsMerged 验证全部空闲时合并水位保持不变，即使时钟继续前进。
func TestAllIdleHoldsMerged(t *testing.T) {
	m := newTestMerger(t, 2, 5)
	reportOK(t, m, 0, 0, 10)
	reportOK(t, m, 1, 0, 20)
	if got := m.Merged(); got != 10 {
		t.Fatalf("merged = %d, want 10", got)
	}

	for _, now := range []Time{5, 6, 100, 1000} {
		got, err := m.AdvanceClock(now)
		if err != nil {
			t.Fatalf("advance %d: %v", now, err)
		}
		if got != 10 {
			t.Fatalf("at t=%d merged = %d, want 10 held with all partitions idle", now, got)
		}
	}
}

// TestIdleRecoveryBelowMerged 验证空闲分区恢复时，上报低于合并水位但不低于
// 自身历史水位的值：分区恢复活跃，但合并水位不得被拉低。
func TestIdleRecoveryBelowMerged(t *testing.T) {
	m := newTestMerger(t, 2, 10)
	reportOK(t, m, 0, 0, 100) // p0 慢
	reportOK(t, m, 1, 0, 100)
	reportOK(t, m, 1, 5, 200) // p1 推进

	// t=10：p0 elapsed=10 空闲；p1 elapsed=5 活跃 -> 合并水位推进到 200。
	if got, _ := m.AdvanceClock(10); got != 200 {
		t.Fatalf("merged at t=10 = %d, want 200", got)
	}
	// t=15：p1 也空闲，全部空闲，合并水位维持 200。
	if got, _ := m.AdvanceClock(15); got != 200 {
		t.Fatalf("merged at t=15 = %d, want 200", got)
	}

	// p0 恢复活跃，上报 150：高于自身历史 100（合法），但低于合并水位 200。
	got, err := m.Report(0, 15, 150)
	if err != nil {
		t.Fatalf("recovery report: %v", err)
	}
	if got != 200 {
		t.Fatalf("merged after low recovery = %d, want 200 (must not regress)", got)
	}
	snap := m.Snapshot()
	if snap.Partitions[0].Idle {
		t.Fatalf("recovered partition p0 must be active: %+v", snap.Partitions[0])
	}
	if snap.Partitions[0].Watermark != 150 {
		t.Fatalf("p0 watermark = %d, want 150", snap.Partitions[0].Watermark)
	}

	// p0 随后追上来：此时 p1 仍空闲，唯一活跃分区 p0=250，合并水位推进到 250。
	got, err = m.Report(0, 16, 250)
	if err != nil || got != 250 {
		t.Fatalf("merged = %d, err %v, want 250 (only active partition p0=250)", got, err)
	}
}

// TestRejectedOperationsLeaveStateUntouched 验证各类非法输入被拒绝，
// 且分区水位、活跃时间、时钟、合并水位均不变，错误原因可用 errors.Is 区分。
func TestRejectedOperationsLeaveStateUntouched(t *testing.T) {
	m := newTestMerger(t, 2, 10)
	reportOK(t, m, 0, 0, 100)
	reportOK(t, m, 1, 0, 200)
	if _, err := m.AdvanceClock(5); err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot()

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"negative report time", func() error { _, err := m.Report(0, -1, 300); return err }, ErrInvalidArgument},
		{"negative report watermark", func() error { _, err := m.Report(0, 6, -1); return err }, ErrInvalidArgument},
		{"negative clock", func() error { _, err := m.AdvanceClock(-1); return err }, ErrInvalidArgument},
		{"partition negative", func() error { _, err := m.Report(-1, 6, 300); return err }, ErrPartitionOutOfRange},
		{"partition too large", func() error { _, err := m.Report(2, 6, 300); return err }, ErrPartitionOutOfRange},
		{"clock rollback via AdvanceClock", func() error { _, err := m.AdvanceClock(4); return err }, ErrClockRollback},
		{"clock rollback via Report", func() error { _, err := m.Report(0, 4, 300); return err }, ErrClockRollback},
		{"partition watermark rollback", func() error { _, err := m.Report(0, 5, 99); return err }, ErrWatermarkRollback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, tc.want)
			}
			after := m.Snapshot()
			if !snapshotsEqual(before, after) {
				t.Fatalf("state changed after rejected %s:\nbefore=%+v\nafter =%+v", tc.name, before, after)
			}
		})
	}
}

// TestInvalidConfig 验证构造参数非法时返回可区分原因。
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero partitions", Config{Partitions: 0, IdleThreshold: 10}},
		{"negative partitions", Config{Partitions: -1, IdleThreshold: 10}},
		{"negative threshold", Config{Partitions: 2, IdleThreshold: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New(%+v) err = %v, want ErrInvalidConfig", tc.cfg, err)
			}
		})
	}
}

// TestSameTimeReports 允许相等时间戳的多次上报（时钟只进不退，相等不违规）。
func TestSameTimeReports(t *testing.T) {
	m := newTestMerger(t, 2, 10)
	if _, err := m.Report(0, 7, 10); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Report(1, 7, 20); err != nil || got != 10 {
		t.Fatalf("same-time report: merged=%d err=%v, want 10", got, err)
	}
	if got, err := m.AdvanceClock(7); err != nil || got != 10 {
		t.Fatalf("same-time advance: merged=%d err=%v, want 10", got, err)
	}
}

// TestDeterminism 同一输入序列在两个实例上反复计算，输出必须完全相同。
func TestDeterminism(t *testing.T) {
	type op struct {
		kind      string
		partition int
		at        Time
		w         Watermark
	}
	script := []op{
		{"report", 0, 0, 100},
		{"report", 1, 0, 200},
		{"clock", 0, 5, 0},
		{"report", 1, 5, 300},
		{"clock", 0, 10, 0},
		{"clock", 0, 20, 0},
		{"report", 0, 20, 400},
	}

	run := func() ([]Watermark, Snapshot) {
		m := newTestMerger(t, 2, 10)
		outs := make([]Watermark, 0, len(script))
		for _, o := range script {
			switch o.kind {
			case "report":
				got, err := m.Report(o.partition, o.at, o.w)
				if err != nil {
					t.Fatalf("report: %v", err)
				}
				outs = append(outs, got)
			case "clock":
				got, err := m.AdvanceClock(o.at)
				if err != nil {
					t.Fatalf("clock: %v", err)
				}
				outs = append(outs, got)
			}
		}
		return outs, m.Snapshot()
	}

	outs1, snap1 := run()
	outs2, snap2 := run()
	if fmt.Sprint(outs1) != fmt.Sprint(outs2) {
		t.Fatalf("nondeterministic outputs: %v vs %v", outs1, outs2)
	}
	if !snapshotsEqual(snap1, snap2) {
		t.Fatalf("nondeterministic snapshots:\n%+v\n%+v", snap1, snap2)
	}
}

// TestConcurrentReaders 并发读取下，每个读者观察到的合并水位单调不减。
// 需配合 `go test -race` 运行以检测数据竞争。
func TestConcurrentReaders(t *testing.T) {
	m := newTestMerger(t, 4, 50)

	const readers = 8
	const rounds = 2000

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：持续读取并记录自己的观察序列。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := m.Merged()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := m.Merged()
				if got < last {
					t.Errorf("reader observed merged regress: %d -> %d", last, got)
					return
				}
				last = got
			}
		}()
	}

	// 写者：交替推进时钟与上报水位；全部空闲时合并水位也只会保持。
	for i := 0; i < rounds; i++ {
		at := Time(i)
		if i%13 == 0 {
			// 偶尔只推进时钟，制造分区空闲窗口。
			if _, err := m.AdvanceClock(at); err != nil {
				t.Fatalf("advance: %v", err)
			}
			continue
		}
		p := i % 4
		if _, err := m.Report(p, at, Watermark(i*3)); err != nil {
			t.Fatalf("report p=%d: %v", p, err)
		}
	}

	close(stop)
	wg.Wait()
}

// TestLoggerOutput 验证日志中包含输入、合并水位与判定依据。
func TestLoggerOutput(t *testing.T) {
	lg := &captureLogger{}
	m, err := New(Config{Partitions: 2, IdleThreshold: 10, Logger: lg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Report(0, 0, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Report(1, 0, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdvanceClock(10); err != nil {
		t.Fatal(err)
	}

	out := lg.String()
	for _, want := range []string{
		"op=report",
		"op=advance_clock",
		"partition=0",
		"reported=100",
		"threshold=10",
		"candidate_min=100",
		"merged 0 -> 100",
		"idle=true",
		"no active partition",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, out)
		}
	}
}

func reportOK(t *testing.T, m *Merger, p int, at Time, w Watermark) {
	t.Helper()
	if _, err := m.Report(p, at, w); err != nil {
		t.Fatalf("Report(%d,%d,%d): %v", p, at, w, err)
	}
}

// snapshotsEqual 比较两个快照的全部可观察字段。
func snapshotsEqual(a, b Snapshot) bool {
	if a.Now != b.Now || a.ClockSet != b.ClockSet || a.Merged != b.Merged ||
		a.IdleThreshold != b.IdleThreshold || len(a.Partitions) != len(b.Partitions) {
		return false
	}
	for i := range a.Partitions {
		if a.Partitions[i] != b.Partitions[i] {
			return false
		}
	}
	return true
}
