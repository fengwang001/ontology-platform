package attempt_test

import (
	"errors"
	"testing"

	"ontology/attempt"
	"ontology/fanout"
	"ontology/matrix"
)

// 规格续例配置：5 个作业，0/1/2 试验性，3/4 非试验性。
func specConfig() attempt.Config {
	return attempt.Config{
		Matrix: matrix.Config{
			Axes: []matrix.Axis{
				{Key: "os", Values: []string{"linux", "win"}},
				{Key: "ver", Values: []string{"1", "2"}},
			},
			Exclude: []map[string]string{{"os": "win", "ver": "1"}},
			Include: []map[string]string{
				{"os": "linux", "tag": "a"},
				{"ver": "2", "tag": "b"},
				{"os": "mac", "ver": "2"},
				{"os": "mac", "tag": "c"},
				{"experimental": "true"},
			},
		},
		P:        2,
		FailFast: true,
		A:        2,
	}
}

func wantStates(t *testing.T, e *attempt.Executor, want ...fanout.State) {
	t.Helper()
	for i, s := range want {
		if got := e.State(i); got != s {
			t.Fatalf("job %d: got %s, want %s", i, got, s)
		}
	}
}

// 规格续例：P=2，failFast，A=2 的完整执行与重跑超限。
func TestSpecContinuation(t *testing.T) {
	e, err := attempt.New(specConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantStates(t, e, fanout.Running, fanout.Running, fanout.Pending, fanout.Pending, fanout.Pending)
	// 试验性失败不触发快停，启动 2。
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, fanout.Failed, fanout.Running, fanout.Running, fanout.Pending, fanout.Pending)
	if err := e.Finish(1, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, fanout.Failed, fanout.Succeeded, fanout.Running, fanout.Running, fanout.Pending)
	// 非试验性失败触发快停：2 由 Running、4 由 Pending 转 Cancelled。
	if err := e.Finish(3, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantStates(t, e, fanout.Failed, fanout.Succeeded, fanout.Cancelled, fanout.Failed, fanout.Cancelled)
	if !e.Terminal() {
		t.Fatal("should be terminal")
	}
	if got := e.Result(); got != fanout.ResultFailed {
		t.Fatalf("got %s, want Failed", got)
	}
	// Rerun：0、2、3、4 置回 Pending 并启动 0、2；1 不再运行。
	if err := e.Rerun(); err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	wantStates(t, e, fanout.Running, fanout.Succeeded, fanout.Running, fanout.Pending, fanout.Pending)
	if e.Runs(1) != 1 || e.Runs(0) != 2 || e.Runs(2) != 2 {
		t.Fatalf("runs: %d %d %d", e.Runs(0), e.Runs(1), e.Runs(2))
	}
	if e.Rounds() != 2 {
		t.Fatalf("rounds: got %d, want 2", e.Rounds())
	}
	// 第二轮仍未全部成功。
	if err := e.Finish(0, false); err != nil { // 试验性，不触发快停
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(2, false); err != nil { // 试验性，不触发快停
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(3, false); err != nil { // 非试验性，快停，4 转 Cancelled
		t.Fatalf("Finish: %v", err)
	}
	if !e.Terminal() || e.Result() != fanout.ResultFailed {
		t.Fatalf("round 2 should end Failed, terminal=%v result=%s", e.Terminal(), e.Result())
	}
	// 已用轮次达到 A=2：重跑超限。
	if err := e.Rerun(); !errors.Is(err, attempt.ErrRerunLimit) {
		t.Fatalf("Rerun: got %v, want ErrRerunLimit", err)
	}
}

// Rerun 范围含试验性的 Failed 与 Cancelled；Succeeded 不再运行。
func TestRerunScope(t *testing.T) {
	cfg := attempt.Config{
		Matrix: matrix.Config{
			Axes: []matrix.Axis{{Key: "os", Values: []string{"a", "b", "c", "d"}}},
			Include: []map[string]string{
				{"os": "a", "experimental": "true"},
				{"os": "b", "experimental": "true"},
			},
		},
		P:        2,
		FailFast: false,
		A:        3,
	}
	e, err := attempt.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 0 试验性失败，1 试验性成功，2 非试验性失败，3 成功。
	for _, f := range []struct {
		i  int
		ok bool
	}{{0, false}, {1, true}, {2, false}, {3, true}} {
		if err := e.Finish(f.i, f.ok); err != nil {
			t.Fatalf("Finish(%d): %v", f.i, err)
		}
	}
	if got := e.Result(); got != fanout.ResultFailed {
		t.Fatalf("got %s, want Failed", got)
	}
	if err := e.Rerun(); err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	// 只有 0、2 重跑；1、3 保持 Succeeded。
	wantStates(t, e, fanout.Running, fanout.Succeeded, fanout.Running, fanout.Succeeded)
	if e.Runs(0) != 2 || e.Runs(1) != 1 || e.Runs(2) != 2 || e.Runs(3) != 1 {
		t.Fatalf("runs: %d %d %d %d", e.Runs(0), e.Runs(1), e.Runs(2), e.Runs(3))
	}
	// 这一轮 0 试验性失败、2 成功 => 整体 Succeeded（试验性失败不影响）。
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(2, true); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := e.Result(); got != fanout.ResultSucceeded {
		t.Fatalf("got %s, want Succeeded", got)
	}
	// Succeeded 后 Rerun 报状态不符。
	if err := e.Rerun(); !errors.Is(err, fanout.ErrState) {
		t.Fatalf("Rerun after success: got %v, want ErrState", err)
	}
}

// 运行期拒绝次序：参数非法 > 状态不符 > 重跑超限。
func TestRuntimeRejectionOrder(t *testing.T) {
	e, err := attempt.New(attempt.Config{
		Matrix: matrix.Config{Axes: []matrix.Axis{{Key: "os", Values: []string{"a", "b"}}}},
		P:      1, A: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 未终结时 Rerun：状态不符（即使轮次也可能超限，状态不符优先）。
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Rerun(); !errors.Is(err, fanout.ErrState) {
		t.Fatalf("Rerun while running: got %v, want ErrState", err)
	}
	// 下标越界优先于状态不符。
	if err := e.Finish(99, true); !errors.Is(err, fanout.ErrParam) {
		t.Fatalf("Finish out of range: got %v, want ErrParam", err)
	}
	if err := e.Finish(1, true); !errors.Is(err, fanout.ErrState) {
		t.Fatalf("Finish pending: got %v, want ErrState", err)
	}
	// 跑完（失败），A=1 => 重跑超限。
	if err := e.Finish(0, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Finish(1, false); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := e.Rerun(); !errors.Is(err, attempt.ErrRerunLimit) {
		t.Fatalf("Rerun at limit: got %v, want ErrRerunLimit", err)
	}
	// 被拒绝的操作不改任何状态。
	wantStates(t, e, fanout.Failed, fanout.Failed)
	if e.Rounds() != 1 {
		t.Fatalf("rounds: got %d, want 1", e.Rounds())
	}
}

// New 的拒绝次序：参数非法 > 矩阵过大 > 空矩阵。
func TestNewRejectionOrder(t *testing.T) {
	base := specConfig()
	for _, p := range []int{0, -1, 257} {
		cfg := base
		cfg.P = p
		if _, err := attempt.New(cfg); !errors.Is(err, matrix.ErrInvalid) {
			t.Fatalf("P=%d: got %v, want ErrInvalid", p, err)
		}
	}
	for _, a := range []int{0, -1, 6} {
		cfg := base
		cfg.A = a
		if _, err := attempt.New(cfg); !errors.Is(err, matrix.ErrInvalid) {
			t.Fatalf("A=%d: got %v, want ErrInvalid", a, err)
		}
	}
	// P 非法且矩阵过大：参数非法优先。
	cfg := base
	cfg.P = 0
	cfg.Matrix.Axes = []matrix.Axis{
		{Key: "a", Values: []string{"1", "2", "3", "4"}},
		{Key: "b", Values: []string{"1", "2", "3", "4"}},
		{Key: "c", Values: []string{"1", "2", "3", "4"}},
		{Key: "d", Values: []string{"1", "2", "3", "4"}},
		{Key: "e", Values: []string{"1", "2"}},
	}
	cfg.Matrix.Exclude = nil
	cfg.Matrix.Include = nil
	if _, err := attempt.New(cfg); !errors.Is(err, matrix.ErrInvalid) {
		t.Fatalf("invalid P + too large: got %v, want ErrInvalid", err)
	}
	// 矩阵过大优先于空矩阵无法同时成立，单独验证过大。
	cfg.P = 1
	if _, err := attempt.New(cfg); !errors.Is(err, matrix.ErrTooLarge) {
		t.Fatalf("512 jobs: got %v, want ErrTooLarge", err)
	}
}
