package fanout

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/matrix"
)

func specConfig() Config {
	return Config{
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
		P: 2, FailFast: true, A: 2,
	}
}

func mustNew(t *testing.T, cfg Config) *Executor {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New 报错: %v", err)
	}
	return e
}

func wantStates(t *testing.T, e *Executor, want ...State) {
	t.Helper()
	if len(want) != e.Len() {
		t.Fatalf("状态数 %d 与作业数 %d 不符", len(want), e.Len())
	}
	for i, w := range want {
		got, err := e.State(i)
		if err != nil {
			t.Fatalf("State(%d) 报错: %v", i, err)
		}
		if got != w {
			t.Fatalf("作业 %d 状态 = %v, want %v", i, got, w)
		}
	}
}

func wantResult(t *testing.T, e *Executor, want State) {
	t.Helper()
	got, done := e.Result()
	if !done {
		t.Fatalf("本轮尚未终结, want %v", want)
	}
	if got != want {
		t.Fatalf("本轮结果 = %v, want %v", got, want)
	}
}

func mustFinish(t *testing.T, e *Executor, i int, ok bool) {
	t.Helper()
	if err := e.Finish(i, ok); err != nil {
		t.Fatalf("Finish(%d, %v) 报错: %v", i, ok, err)
	}
}

// TestSpecWalkthrough 逐步走查题目续例（P=2，failFast=true，A=2）。
func TestSpecWalkthrough(t *testing.T) {
	e := mustNew(t, specConfig())
	if e.Len() != 5 {
		t.Fatalf("作业数 = %d, want 5", e.Len())
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	wantStates(t, e, Running, Running, Pending, Pending, Pending)

	// Finish(0, 假)：试验性失败，不触发快停，启动 2。
	mustFinish(t, e, 0, false)
	wantStates(t, e, Failed, Running, Running, Pending, Pending)

	// Finish(1, 真)：启动 3。
	mustFinish(t, e, 1, true)
	wantStates(t, e, Failed, Succeeded, Running, Running, Pending)

	// Finish(3, 假)：非试验性失败触发快停，2 由 Running、4 由 Pending 转 Cancelled。
	mustFinish(t, e, 3, false)
	wantStates(t, e, Failed, Succeeded, Cancelled, Failed, Cancelled)
	wantResult(t, e, Failed)

	// 快停后迟到的 Finish 报状态不符。
	if err := e.Finish(2, true); !errors.Is(err, ErrState) {
		t.Fatalf("快停后对 Cancelled 作业 Finish 应报状态不符, got %v", err)
	}
	if err := e.Finish(4, false); !errors.Is(err, ErrState) {
		t.Fatalf("快停后对 Cancelled 作业 Finish 应报状态不符, got %v", err)
	}

	// Rerun：0、2、3、4 置回 Pending 并启动 0、2，作业 1 不再运行。
	if err := e.Rerun(); err != nil {
		t.Fatalf("Rerun 报错: %v", err)
	}
	wantStates(t, e, Running, Succeeded, Running, Pending, Pending)
	for i, want := range []int{2, 1, 2, 1, 0} {
		got, err := e.Runs(i)
		if err != nil {
			t.Fatalf("Runs(%d) 报错: %v", i, err)
		}
		if got != want {
			t.Fatalf("作业 %d 进入 Running 次数 = %d, want %d", i, got, want)
		}
	}
	if e.Round() != 2 {
		t.Fatalf("轮次 = %d, want 2", e.Round())
	}

	// 第二轮仍未全部成功：再 Rerun 报重跑超限。
	mustFinish(t, e, 0, true)
	mustFinish(t, e, 3, false)
	wantStates(t, e, Succeeded, Succeeded, Cancelled, Failed, Cancelled)
	wantResult(t, e, Failed)
	if err := e.Rerun(); !errors.Is(err, ErrRerunExhausted) {
		t.Fatalf("已用轮次达到 A=2 应报重跑超限, got %v", err)
	}
	t.Logf("输入=规格续例 输出=两轮均 Failed 判定依据=非试验性失败触发快停且重跑含试验性失败作业")
}

func twoJobConfig(expA, expB bool, failFast bool, a int) Config {
	inc := []map[string]string{}
	if expA {
		inc = append(inc, map[string]string{"os": "a", "experimental": "true"})
	}
	if expB {
		inc = append(inc, map[string]string{"os": "b", "experimental": "true"})
	}
	return Config{
		Matrix: matrix.Config{
			Axes:    []matrix.Axis{{Key: "os", Values: []string{"a", "b"}}},
			Include: inc,
		},
		P: 2, FailFast: failFast, A: a,
	}
}

func TestExperimentalFailureNoFailFast(t *testing.T) {
	// 两个作业均为试验性，failFast=true：试验性失败不触发快停，也不影响本轮结果。
	e := mustNew(t, twoJobConfig(true, true, true, 1))
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	mustFinish(t, e, 0, false)
	wantStates(t, e, Failed, Running)
	mustFinish(t, e, 1, false)
	wantStates(t, e, Failed, Failed)
	wantResult(t, e, Succeeded)
	// 结果为 Succeeded，Rerun 报状态不符。
	if err := e.Rerun(); !errors.Is(err, ErrState) {
		t.Fatalf("Succeeded 后 Rerun 应报状态不符, got %v", err)
	}
	t.Logf("输入=两试验性作业均失败 输出=Succeeded 判定依据=试验性失败不影响本轮结果")
}

func TestRerunIncludesExperimentalFailure(t *testing.T) {
	// 作业 0 试验性、作业 1 非试验性，failFast=false：重跑范围含试验性失败。
	e := mustNew(t, twoJobConfig(true, false, false, 2))
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	mustFinish(t, e, 0, false)
	mustFinish(t, e, 1, false)
	wantStates(t, e, Failed, Failed)
	wantResult(t, e, Failed)
	if err := e.Rerun(); err != nil {
		t.Fatalf("Rerun 报错: %v", err)
	}
	wantStates(t, e, Running, Running)
	mustFinish(t, e, 0, true)
	mustFinish(t, e, 1, true)
	wantResult(t, e, Succeeded)
	t.Logf("输入=试验性+非试验性均失败 输出=重跑后全部 Succeeded 判定依据=非 Succeeded 作业全部置回 Pending")
}

func TestRerunExhausted(t *testing.T) {
	// A=1：第一轮终结后立即超限。
	e := mustNew(t, twoJobConfig(false, false, false, 1))
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	mustFinish(t, e, 0, false)
	mustFinish(t, e, 1, true)
	wantResult(t, e, Failed)
	if err := e.Rerun(); !errors.Is(err, ErrRerunExhausted) {
		t.Fatalf("A=1 时 Rerun 应报重跑超限, got %v", err)
	}
}

func bigMatrix() matrix.Config {
	vals := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	return matrix.Config{Axes: []matrix.Axis{
		{Key: "a", Values: vals},
		{Key: "b", Values: vals},
		{Key: "c", Values: vals},
	}}
}

func emptyMatrix() matrix.Config {
	return matrix.Config{
		Axes:    []matrix.Axis{{Key: "os", Values: []string{"a", "b"}}},
		Exclude: []map[string]string{{"os": "a"}, {"os": "b"}},
	}
}

func TestNewRejectOrder(t *testing.T) {
	small := matrix.Config{Axes: []matrix.Axis{{Key: "os", Values: []string{"a"}}}}
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"P=0 参数非法", Config{Matrix: small, P: 0, A: 1}, ErrInvalidParam},
		{"P=257 参数非法", Config{Matrix: small, P: 257, A: 1}, ErrInvalidParam},
		{"A=0 参数非法", Config{Matrix: small, P: 1, A: 0}, ErrInvalidParam},
		{"A=6 参数非法", Config{Matrix: small, P: 1, A: 6}, ErrInvalidParam},
		{"参数非法优先于矩阵过大", Config{Matrix: bigMatrix(), P: 0, A: 1}, ErrInvalidParam},
		{"矩阵过大", Config{Matrix: bigMatrix(), P: 1, A: 1}, ErrTooLarge},
		{"空矩阵", Config{Matrix: emptyMatrix(), P: 1, A: 1}, ErrEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRuntimeRejectOrder(t *testing.T) {
	e := mustNew(t, twoJobConfig(false, false, false, 1))
	// 参数非法（下标越界）> 状态不符：未启动时越界下标报参数非法。
	if err := e.Finish(-1, true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("下标 -1 应报参数非法, got %v", err)
	}
	if err := e.Finish(2, true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("下标越界应报参数非法, got %v", err)
	}
	// 未启动时合法下标报状态不符。
	if err := e.Finish(0, true); !errors.Is(err, ErrState) {
		t.Fatalf("非 Running 作业 Finish 应报状态不符, got %v", err)
	}
	// 状态不符 > 重跑超限：本轮未终结时 Rerun 报状态不符而非超限（A=1）。
	if err := e.Rerun(); !errors.Is(err, ErrState) {
		t.Fatalf("未终结时 Rerun 应报状态不符, got %v", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	// Start 只能调一次。
	if err := e.Start(); !errors.Is(err, ErrState) {
		t.Fatalf("重复 Start 应报状态不符, got %v", err)
	}
	mustFinish(t, e, 0, false)
	// 终结后对 Failed 作业再 Finish 报状态不符。
	if err := e.Finish(0, true); !errors.Is(err, ErrState) {
		t.Fatalf("终结后对 Failed 作业 Finish 应报状态不符, got %v", err)
	}
	mustFinish(t, e, 1, true)
	wantResult(t, e, Failed)
	// 已终结且非 Succeeded，但已用轮次达到 A=1：报重跑超限。
	if err := e.Rerun(); !errors.Is(err, ErrRerunExhausted) {
		t.Fatalf("应报重跑超限, got %v", err)
	}
	// 被拒绝的操作不改任何状态。
	wantStates(t, e, Failed, Succeeded)
}

func TestCancelAll(t *testing.T) {
	// 未启动时 CancelAll 为无操作。
	e := mustNew(t, twoJobConfig(false, false, false, 2))
	e.CancelAll()
	wantStates(t, e, Pending, Pending)
	if _, done := e.Result(); done {
		t.Fatalf("未启动时不应终结")
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	e.CancelAll()
	wantStates(t, e, Cancelled, Cancelled)
	wantResult(t, e, Cancelled)
	// 已终结时 CancelAll 为无操作。
	e.CancelAll()
	wantStates(t, e, Cancelled, Cancelled)
	// Cancelled 结果可重跑。
	if err := e.Rerun(); err != nil {
		t.Fatalf("Rerun 报错: %v", err)
	}
	wantStates(t, e, Running, Running)
	t.Logf("输入=CancelAll 输出=Cancelled 判定依据=存在 Cancelled 且无非试验性 Failed")
}

func TestResultPriority(t *testing.T) {
	// 非试验性 Failed 优先于 Cancelled。
	e := mustNew(t, twoJobConfig(false, false, false, 1))
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	mustFinish(t, e, 0, false)
	e.CancelAll()
	wantStates(t, e, Failed, Cancelled)
	wantResult(t, e, Failed)
	t.Logf("输入=Failed+Cancelled 输出=Failed 判定依据=结果次序 Failed>Cancelled>Succeeded")
}

func TestConcurrentFinish(t *testing.T) {
	// 并发 Finish 等价于某个串行顺序：最终全部 Succeeded。
	vals := []string{"0", "1", "2", "3", "4", "5", "6", "7"}
	e := mustNew(t, Config{
		Matrix: matrix.Config{Axes: []matrix.Axis{
			{Key: "a", Values: vals},
			{Key: "b", Values: vals},
		}},
		P: 4, A: 1,
	})
	if err := e.Start(); err != nil {
		t.Fatalf("Start 报错: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, done := e.Result(); done {
					return
				}
				progressed := false
				for i := 0; i < e.Len(); i++ {
					s, err := e.State(i)
					if err == nil && s == Running {
						if err := e.Finish(i, true); err == nil {
							progressed = true
						}
					}
				}
				if !progressed {
					if _, done := e.Result(); done {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	wantResult(t, e, Succeeded)
	for i := 0; i < e.Len(); i++ {
		runs, err := e.Runs(i)
		if err != nil || runs != 1 {
			t.Fatalf("作业 %d 运行次数 = %d, want 1 (err=%v)", i, runs, err)
		}
	}
}

func ExampleExecutor() {
	e, _ := New(specConfig())
	_ = e.Start()
	_ = e.Finish(0, false)
	_ = e.Finish(1, true)
	_ = e.Finish(3, false)
	res, _ := e.Result()
	fmt.Println(res)
	// Output: Failed
}
