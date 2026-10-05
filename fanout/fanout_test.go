package fanout

import (
	"errors"
	"testing"
)

func expFlags(n int, exp ...int) []bool {
	flags := make([]bool, n)
	for _, i := range exp {
		flags[i] = true
	}
	return flags
}

func wantStates(t *testing.T, e *Executor, want ...State) {
	t.Helper()
	for i, s := range want {
		if got := e.State(i); got != s {
			t.Fatalf("job %d: got %s, want %s", i, got, s)
		}
	}
}

func TestBasicFlow(t *testing.T) {
	e := NewExecutor(3, expFlags(3), 2, false)
	if e.Terminal() {
		t.Fatal("not started, must not be terminal")
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantStates(t, e, Running, Running, Pending)
	if err := e.Start(); !errors.Is(err, ErrState) {
		t.Fatalf("second Start: got %v, want ErrState", err)
	}
	if err := e.Finish(0, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, Succeeded, Running, Running)
	if err := e.Finish(1, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if e.Terminal() {
		t.Fatal("job 2 still running")
	}
	if err := e.Finish(2, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !e.Terminal() {
		t.Fatal("should be terminal")
	}
	if got := e.Result(); got != ResultFailed {
		t.Fatalf("got %s, want Failed", got)
	}
	if e.Runs(0) != 1 || e.Runs(2) != 1 {
		t.Fatalf("runs: %d %d", e.Runs(0), e.Runs(2))
	}
}

// 拒绝次序：下标越界（参数非法）优先于状态不符。
func TestFinishRejectionOrder(t *testing.T) {
	e := NewExecutor(2, expFlags(2), 1, false)
	if err := e.Finish(5, true); !errors.Is(err, ErrParam) {
		t.Fatalf("before start, bad index: got %v, want ErrParam", err)
	}
	if err := e.Finish(-1, true); !errors.Is(err, ErrParam) {
		t.Fatalf("negative index: got %v, want ErrParam", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Finish(1, true); !errors.Is(err, ErrState) {
		t.Fatalf("pending job: got %v, want ErrState", err)
	}
	if err := e.Finish(0, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(0, true); !errors.Is(err, ErrState) {
		t.Fatalf("succeeded job: got %v, want ErrState", err)
	}
	// 被拒绝的操作不改任何状态。
	wantStates(t, e, Succeeded, Running)
}

// 试验性作业失败不触发快停。
func TestExperimentalFailNoFailFast(t *testing.T) {
	e := NewExecutor(3, expFlags(3, 0), 2, true)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, Failed, Running, Running)
	if err := e.Finish(1, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(2, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := e.Result(); got != ResultSucceeded {
		t.Fatalf("experimental failure must not fail the round: got %s", got)
	}
}

// 非试验性失败且 failFast：其余 Running 与 Pending 立即转 Cancelled，
// 没有确认阶段，迟到的 Finish 报状态不符。
func TestFailFastCancelsImmediately(t *testing.T) {
	e := NewExecutor(4, expFlags(4), 2, true)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, Failed, Cancelled, Cancelled, Cancelled)
	if !e.Terminal() {
		t.Fatal("should be terminal after fail-fast")
	}
	if got := e.Result(); got != ResultFailed {
		t.Fatalf("got %s, want Failed", got)
	}
	// 迟到的 Finish（此前为 Running 与 Pending）报状态不符。
	if err := e.Finish(1, true); !errors.Is(err, ErrState) {
		t.Fatalf("late Finish on cancelled running job: got %v, want ErrState", err)
	}
	if err := e.Finish(3, false); !errors.Is(err, ErrState) {
		t.Fatalf("late Finish on cancelled pending job: got %v, want ErrState", err)
	}
}

// 结果判定次序：非试验性 Failed > Cancelled > Succeeded。
func TestResultPrecedence(t *testing.T) {
	// 试验性 Failed + Cancelled => Cancelled。
	e := NewExecutor(2, expFlags(2, 0), 2, false)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	e.CancelAll()
	if got := e.Result(); got != ResultCancelled {
		t.Fatalf("got %s, want Cancelled", got)
	}
	// 非试验性 Failed + Cancelled => Failed。
	e = NewExecutor(2, expFlags(2), 2, false)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	e.CancelAll()
	if got := e.Result(); got != ResultFailed {
		t.Fatalf("got %s, want Failed", got)
	}
}

func TestCancelAll(t *testing.T) {
	e := NewExecutor(3, expFlags(3), 1, false)
	e.CancelAll() // 未启动：无操作
	wantStates(t, e, Pending, Pending, Pending)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	e.CancelAll()
	wantStates(t, e, Cancelled, Cancelled, Cancelled)
	if !e.Terminal() {
		t.Fatal("should be terminal")
	}
	if got := e.Result(); got != ResultCancelled {
		t.Fatalf("got %s, want Cancelled", got)
	}
	e.CancelAll() // 已终结：无操作
	wantStates(t, e, Cancelled, Cancelled, Cancelled)
}

// scanned 证明：单次 Finish 为寻找下一个待启动作业而检视的作业数
// 不超过 1，与 n 无关（n=16 与 n=256 两档对照）。
func TestScannedPerFinish(t *testing.T) {
	for _, n := range []int{16, 256} {
		t.Run(string(rune('0' + n))[:0]+itoa(n), func(t *testing.T) {
			e := NewExecutor(n, expFlags(n), 4, false)
			if err := e.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			for i := 0; i < n; i++ {
				before := e.Scanned()
				if err := e.Finish(i, true); err != nil {
					t.Fatalf("Finish(%d): %v", i, err)
				}
				if got := e.Scanned() - before; got > 1 {
					t.Fatalf("n=%d Finish(%d) scanned %d jobs, want <= 1", n, i, got)
				}
			}
			if !e.Terminal() {
				t.Fatal("should be terminal")
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// 不变式：任一时刻 Running 数不超过 P；Start 之后只要存在 Pending，
// Running 数恰为 P。
func TestRunningInvariant(t *testing.T) {
	for _, p := range []int{1, 3, 256} {
		n := 20
		e := NewExecutor(n, expFlags(n), p, false)
		if err := e.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		check := func() {
			running, pending := 0, 0
			for i := 0; i < n; i++ {
				switch e.State(i) {
				case Running:
					running++
				case Pending:
					pending++
				}
			}
			if running > p {
				t.Fatalf("p=%d: running=%d exceeds P", p, running)
			}
			if pending > 0 && running != min(p, n) {
				t.Fatalf("p=%d: pending=%d but running=%d, want %d", p, pending, running, min(p, n))
			}
		}
		check()
		for i := 0; i < n; i++ {
			if err := e.Finish(i, i%3 == 0); err != nil {
				t.Fatalf("Finish(%d): %v", i, err)
			}
			check()
		}
	}
}

// ResetForRerun：非 Succeeded 置回 Pending，立即启动下标最小的
// min(P, 个数) 个；Succeeded 不再运行。
func TestResetForRerun(t *testing.T) {
	e := NewExecutor(5, expFlags(5), 2, true)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 0 成功，1 失败触发快停 => 2、3、4 Cancelled。
	if err := e.Finish(0, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(1, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, Succeeded, Failed, Cancelled, Cancelled, Cancelled)
	e.ResetForRerun()
	wantStates(t, e, Succeeded, Running, Running, Pending, Pending)
	if e.Runs(0) != 1 || e.Runs(1) != 2 || e.Runs(2) != 2 {
		t.Fatalf("runs: %d %d %d", e.Runs(0), e.Runs(1), e.Runs(2))
	}
	// 下一轮 Finish 继续从下标最小的 Pending 启动。
	if err := e.Finish(1, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, Succeeded, Succeeded, Running, Running, Pending)
}
