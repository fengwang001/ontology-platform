package watermark

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMain(m *testing.M) {
	// 组件始终记录决策日志；用例通过捕获 logger 逐字段校验其内容。
	// 非 -v 运行时静默默认 logger，避免并发用例的大量日志刷屏。
	flag.Parse()
	if !testing.Verbose() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	os.Exit(m.Run())
}

// captureLogger 返回写入 buffer 的 JSON logger，去掉时间字段以便逐字节比较，
// 从而验证“同一输入序列反复计算得到完全相同的输出（含日志）”。
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a

		},
	})
	return slog.New(h), &buf
}

// idle 判定断言：未上报天然空闲；已上报分区在恰等于阈值时空闲，差 1 时不空闲。
func TestIdleBoundaryExactlyEqual(t *testing.T) {
	m, err := New(2, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := m.Report(0, 100, 0); err != nil {
		t.Fatalf("report: %v", err)
	}

	// 差 9：未达阈值，非空闲。
	if err := m.AdvanceClock(9); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, _, parts := m.Snapshot(); parts[0].Idle {
		t.Fatalf("at clock-lastActive=9 partition should be active, snapshot=%+v", parts[0])
	}

	// 恰好等于阈值 10：判定为空闲。
	if err := m.AdvanceClock(10); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, _, parts := m.Snapshot(); !parts[0].Idle {
		t.Fatalf("at clock-lastActive==threshold partition must be idle, snapshot=%+v", parts[0])
	}
}

// 端到端主线：多分区取最小推进、空闲分区被剔除使合并水位越过它前进、
// 全部空闲保持不变、空闲分区以低于合并水位的值恢复不会拉退水位。
func TestMergeAdvanceIdleAndRecover(t *testing.T) {
	m, _ := New(3, 10)

	mustReport := func(p int, wm, now int64) {
		t.Helper()
		if err := m.Report(p, wm, now); err != nil {
			t.Fatalf("Report(%d,%d,%d): %v", p, wm, now, err)
		}
	}
	mustAdvance := func(now int64) {
		t.Helper()
		if err := m.AdvanceClock(now); err != nil {
			t.Fatalf("AdvanceClock(%d): %v", now, err)
		}
	}
	expectMerged := func(want int64) {
		t.Helper()
		if got := m.Watermark(); got != want {
			_, _, parts := m.Snapshot()
			t.Fatalf("merged = %d, want %d; partitions=%+v", got, want, parts)
		}
	}

	// 候选取非空闲分区水位最小值：min(50,60)=50。
	mustReport(1, 50, 0)
	mustReport(2, 60, 0)
	expectMerged(50)

	// p2 在 t=5 继续前进到 70；p1 仍以 50 卡住整体水位。
	mustReport(2, 70, 5)
	expectMerged(50)

	// t=10：p1 的 lastActive=0，差值恰好等于阈值 -> 空闲被剔除；
	// p2 的 lastActive=5，差值 5 < 阈值，仍活跃，合并水位越过 p1 前进到 70。
	mustAdvance(10)
	expectMerged(70)
	if _, _, parts := m.Snapshot(); !parts[1].Idle || parts[2].Idle {
		t.Fatalf("idle mask wrong at t=10: %+v", parts)
	}

	// t=20：p2 差值 15 >= 阈值，全部空闲，合并水位保持 70 不变。
	mustAdvance(20)
	expectMerged(70)

	// 空闲分区 p1 恢复，但上报值 55 低于当前合并水位 70：
	// 候选最小值虽为 55，合并水位只进不退，仍为 70。
	mustReport(1, 55, 20)
	expectMerged(70)
	if _, _, parts := m.Snapshot(); parts[1].Idle {
		t.Fatalf("p1 should be active again after report: %+v", parts[1])
	}

	// 恢复后的分区继续前进，推动合并水位到 80。
	mustReport(1, 80, 21)
	expectMerged(80)
}

// 全部空闲 / 从无上报：合并水位保持不变（初始为 0），时钟推进本身合法。
func TestAllIdleKeepsMerged(t *testing.T) {
	m, _ := New(2, 3)

	if err := m.AdvanceClock(100); err != nil {
		t.Fatalf("advance with no reports: %v", err)
	}
	if got := m.Watermark(); got != 0 {
		t.Fatalf("merged before any report = %d, want 0", got)
	}

	if err := m.Report(0, 10, 100); err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := m.Report(1, 20, 100); err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := m.Watermark(); got != 10 {
		t.Fatalf("merged = %d, want 10", got)
	}

	// 两个分区在 t=103 恰好同时达到空闲边界，候选集为空 -> 保持 10。
	if err := m.AdvanceClock(103); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got := m.Watermark(); got != 10 {
		t.Fatalf("merged after all idle = %d, want 10", got)
	}
}

// 各类非法输入：错误可用 errors.Is 区分，且被拒绝后任何状态都不改变。
func TestInvalidInputsRejectedWithoutStateChange(t *testing.T) {
	t.Run("bad constructor args", func(t *testing.T) {
		cases := []struct {
			n  int
			th int64
		}{
			{0, 1}, {-1, 1}, {2, 0}, {2, -1},
		}
		for _, c := range cases {
			if _, err := New(c.n, c.th); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("New(%d,%d) err=%v, want ErrInvalidArgument", c.n, c.th, err)
			}
		}
	})

	t.Run("partition out of range", func(t *testing.T) {
		m, _ := New(2, 5)
		before := snapshot(m)
		for _, p := range []int{-1, 2, 100} {
			err := m.Report(p, 1, 0)
			if !errors.Is(err, ErrPartitionOutOfRange) {
				t.Fatalf("Report(%d) err=%v, want ErrPartitionOutOfRange", p, err)
			}
		}
		assertUnchanged(t, m, before)
	})

	t.Run("clock backward on report", func(t *testing.T) {
		m, _ := New(2, 5)
		if err := m.Report(0, 10, 10); err != nil {
			t.Fatal(err)
		}
		before := snapshot(m)
		err := m.Report(0, 11, 9)
		if !errors.Is(err, ErrClockBackward) {
			t.Fatalf("err=%v, want ErrClockBackward", err)
		}
		assertUnchanged(t, m, before)
	})

	t.Run("clock backward on advance", func(t *testing.T) {
		m, _ := New(2, 5)
		if err := m.AdvanceClock(10); err != nil {
			t.Fatal(err)
		}
		before := snapshot(m)
		err := m.AdvanceClock(9)
		if !errors.Is(err, ErrClockBackward) {
			t.Fatalf("err=%v, want ErrClockBackward", err)
		}
		assertUnchanged(t, m, before)
	})

	t.Run("partition watermark backward", func(t *testing.T) {
		m, _ := New(2, 5)
		if err := m.Report(0, 10, 0); err != nil {
			t.Fatal(err)
		}
		before := snapshot(m)
		// 时钟前进、但分区水位回退：必须拒绝，且时钟也不得被本次调用推进。
		err := m.Report(0, 9, 5)
		if !errors.Is(err, ErrWatermarkBackward) {
			t.Fatalf("err=%v, want ErrWatermarkBackward", err)
		}
		assertUnchanged(t, m, before)
	})

	t.Run("equal clock and equal watermark allowed", func(t *testing.T) {
		m, _ := New(1, 5)
		if err := m.Report(0, 10, 0); err != nil {
			t.Fatal(err)
		}
		if err := m.Report(0, 10, 0); err != nil {
			t.Fatalf("equal clock/watermark should be allowed, got %v", err)
		}
		if err := m.AdvanceClock(0); err != nil {
			t.Fatalf("equal clock advance should be allowed, got %v", err)
		}
	})
}

type fullSnapshot struct {
	clock      int64
	merged     int64
	partitions []PartitionSnapshot
}

func snapshot(m *Merger) fullSnapshot {
	c, wm, ps := m.Snapshot()
	return fullSnapshot{c, wm, ps}
}

func assertUnchanged(t *testing.T, m *Merger, before fullSnapshot) {
	t.Helper()
	after := snapshot(m)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("state changed by rejected operation:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// 确定性：同一输入序列（含被拒绝的非法操作）在两个全新实例上重放，
// 逐步快照与去时间戳后的日志必须完全一致。
func TestDeterministicReplay(t *testing.T) {
	type step struct {
		op        string // "r" report / "a" advance
		p         int
		wm, now   int64
		wantError bool
	}
	script := []step{
		{"r", 0, 100, 0, false}, // p0 活跃，p1 未上报，合并 100
		{"r", 1, 50, 5, false},  // 两分区活跃，取最小，合并回退？不——取 50<100，
		// 首次两分区都在，候选最小为 50，但合并水位只进不退，故保持 100。
		{"a", -1, 0, 10, false},  // p0 恰好达到空闲边界被剔除，p1 仍活跃，候选 50，保持 100
		{"a", -1, 0, 15, false},  // p1 也空闲，全部空闲，保持 100
		{"r", 1, 90, 15, false},  // 空闲分区恢复但 90<合并水位 100，只参与取小，不拉退
		{"r", 1, 80, 15, true},   // 低于该分区已有水位 90，拒绝
		{"r", 1, 90, 14, true},   // 时钟回退，拒绝
		{"r", 5, 1, 15, true},    // 越界，拒绝
		{"r", 1, 110, 16, false}, // 恢复分区推动合并水位到 110
	}

	run := func() (string, []string) {
		lg, buf := captureLogger()
		m, err := New(2, 10)
		if err != nil {
			t.Fatal(err)
		}
		m.logger = lg

		var snaps []string
		for i, s := range script {
			var e error
			switch s.op {
			case "r":
				e = m.Report(s.p, s.wm, s.now)
			case "a":
				e = m.AdvanceClock(s.now)
			}
			if (e != nil) != s.wantError {
				t.Fatalf("step %d (%+v): err=%v, wantError=%v", i, s, e, s.wantError)
			}
			c, wm, ps := m.Snapshot()
			snaps = append(snaps, fmt.Sprintf("%d|%d|%+v", c, wm, ps))
		}
		logBytes, err := io.ReadAll(buf)
		if err != nil {
			t.Fatal(err)
		}
		return string(logBytes), snaps
	}

	log1, snaps1 := run()
	log2, snaps2 := run()
	if fmt.Sprint(snaps1) != fmt.Sprint(snaps2) {
		t.Fatalf("snapshots differ between replays:\n%v\n%v", snaps1, snaps2)
	}
	if log1 != log2 {
		t.Fatalf("logs differ between replays:\n%s\n----\n%s", log1, log2)
	}

	// 日志必须包含输入、合并水位与判定依据等关键字段。
	for _, want := range []string{
		`"op":"report"`, `"input_watermark":50`, `"merged":100`,
		`"idle_partitions":[0]`, `"active_partitions":[1]`,
		`"candidate_min":90`, `"candidate_min":110`, `"merged":110`,
		`"op":"advance_clock"`,
		`"reason"`, // 被拒绝操作记录原因
	} {
		if !bytes.Contains([]byte(log1), []byte(want)) {
			t.Fatalf("log missing %q, full log:\n%s", want, log1)
		}
	}
}

// 并发：多分区并发上报（全局时钟原子递增只进不退，每个分区的水位由
// 单一写者递增只进不退），并发读者读到的合并水位必须单调不减。
// 配合 go test -race 检测数据竞争。
func TestConcurrentReadersObserveMonotonicWatermark(t *testing.T) {
	m, _ := New(4, 1_000_000) // 阈值取大，测试窗口内不触发空闲，聚焦并发读写
	var clock atomic.Int64
	var stop atomic.Bool
	// “取全局时钟 + 上报”必须作为一个原子步骤，否则两个 goroutine 拿到的
	// 时钟值与实际 Report 顺序可能交错，被正确地判定为时钟回退。
	var submit sync.Mutex

	var writers sync.WaitGroup
	for p := 0; p < 4; p++ {
		writers.Add(1)
		go func(p int) {
			defer writers.Done()
			var wm int64
			for i := 0; i < 500; i++ {
				wm++ // 每个分区单一写者，水位严格只进不退
				submit.Lock()
				now := clock.Add(1)
				err := m.Report(p, wm, now)
				submit.Unlock()
				if err != nil {
					t.Errorf("Report(%d,%d,%d): %v", p, wm, now, err)
					return
				}
			}
		}(p)
	}

	var readers sync.WaitGroup
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			last := m.Watermark()
			for !stop.Load() {
				cur := m.Watermark()
				if cur < last {
					t.Errorf("merged watermark went backwards: %d -> %d", last, cur)
					return
				}
				last = cur
			}
		}()
	}

	writers.Wait()
	stop.Store(true)
	readers.Wait()
}
