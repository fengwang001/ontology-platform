package scaler

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func eval(t *testing.T, s *Scaler, now, metric, wantReplicas int64, wantDecision Decision) {
	t.Helper()
	got, decision, err := s.Evaluate(now, metric)
	if err != nil {
		t.Fatalf("Evaluate(%d,%d) unexpected error: %v", now, metric, err)
	}
	if got != wantReplicas || decision != wantDecision {
		t.Fatalf("Evaluate(%d,%d) = (%d,%s), want (%d,%s)",
			now, metric, got, decision, wantReplicas, wantDecision)
	}
}

// 恰为 10% 的容忍边界（含两侧）推荐值等于当前副本数。
func TestToleranceBoundary(t *testing.T) {
	s, err := New(1, 100, 100, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// T=100，10% 为 10：m=90 与 m=110 恰在边界上，应维持。
	eval(t, s, 0, 90, 1, DecisionHold)
	eval(t, s, 1, 110, 1, DecisionHold)
	// 偏差超出 10% 后触发推荐；下限夹取后仍为 1 时表现为维持。
	eval(t, s, 2, 89, 1, DecisionHold) // ceil(1*89/100)=1，夹到下限后仍为 1
	eval(t, s, 3, 111, 2, DecisionScaleUp)

	// T=105 不能被 10 整除：|m-T|*10<=T 等价于 diff<=floor(105/10)=10。
	s2, err := New(1, 100, 105, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s2, 0, 95, 1, DecisionHold)  // diff=10，恰为边界
	eval(t, s2, 1, 115, 1, DecisionHold) // diff=10，恰为边界
	eval(t, s2, 2, 116, 2, DecisionScaleUp)
}

// 扩容一次不超过翻倍，连续扩容逐步逼近推荐值。
func TestScaleUpDoubleCap(t *testing.T) {
	s, err := New(1, 1000, 100, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s, 0, 100, 1, DecisionHold)
	eval(t, s, 1, 800, 2, DecisionScaleUp) // 推荐 8，翻倍限速到 2
	eval(t, s, 2, 800, 4, DecisionScaleUp) // 推荐 16，限速到 4
	eval(t, s, 3, 800, 8, DecisionScaleUp) // 推荐 32，限速到 8
	eval(t, s, 4, 88, 8, DecisionHold)     // 偏差在 10% 内，推荐保持 8
}

// 恰差 W 的历史仍在窗口内，差 W+1 移出窗口。
func TestWindowBoundary(t *testing.T) {
	s, err := New(1, 100, 100, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	// t=0 扩到 2（历史记入推荐 2）。
	eval(t, s, 0, 200, 2, DecisionScaleUp)
	// t=1 指标归零，推荐 1；t=0 的高值在窗口内压住。
	eval(t, s, 1, 0, 2, DecisionWindowBlocked)
	// t=1000 时 t=0 恰差 W 仍在窗口；本次推荐 1 不覆盖高值，继续压住。
	eval(t, s, 1000, 0, 2, DecisionWindowBlocked)
	// t=1001 时 t=0 移出，窗口内只剩值 1，缩到 1。
	eval(t, s, 1001, 0, 1, DecisionScaleDown)
	// 再冲高到 2（历史记入 2），随即回落。
	eval(t, s, 1002, 200, 2, DecisionScaleUp)
	eval(t, s, 1003, 0, 2, DecisionWindowBlocked)
	// t=2002 恰差 W=1000，t=1002 的高值仍在窗口。
	eval(t, s, 2002, 0, 2, DecisionWindowBlocked)
	// t=2003 差 W+1，移出窗口后缩容。
	eval(t, s, 2003, 0, 1, DecisionScaleDown)
}

// 恰差 D 允许缩容，差 D-1 被冷却压住。
func TestCooldownBoundary(t *testing.T) {
	s, err := New(1, 100, 100, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s, 0, 200, 2, DecisionScaleUp)
	eval(t, s, 1, 0, 1, DecisionScaleDown) // 首次缩容不受冷却限制
	eval(t, s, 2, 200, 2, DecisionScaleUp)
	eval(t, s, 1001, 0, 1, DecisionScaleDown) // 距上次缩容恰为 D

	s2, err := New(1, 100, 100, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s2, 0, 200, 2, DecisionScaleUp)
	eval(t, s2, 10, 0, 1, DecisionScaleDown)
	eval(t, s2, 11, 200, 2, DecisionScaleUp)
	eval(t, s2, 1009, 0, 2, DecisionCooldownBlocked) // 差 D-1
	eval(t, s2, 1010, 0, 1, DecisionScaleDown)       // 恰差 D
}

// 窗口内的历史高值在其存活期内持续压住缩容。
func TestWindowHighWatermark(t *testing.T) {
	s, err := New(4, 100, 100, 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// 下限 4，指标 400 -> 推荐 16，翻倍限速到 8。
	eval(t, s, 0, 400, 8, DecisionScaleUp)
	// 指标归零，窗口高值 8 压住缩容。
	eval(t, s, 10, 0, 8, DecisionWindowBlocked)
	eval(t, s, 50, 0, 8, DecisionWindowBlocked)
	// 指标恢复但未超窗口高值，副本保持稳定。
	// 指标恢复、推荐 16 超过当前，直接扩容（扩容不看窗口）。
	eval(t, s, 60, 200, 16, DecisionScaleUp)
	// 再次回落：窗口高值 16 继续压住缩容。
	eval(t, s, 70, 0, 16, DecisionWindowBlocked)
}

// 推荐值始终被夹到上下限之内。
func TestClampToBounds(t *testing.T) {
	s, err := New(3, 10, 100, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Replicas(); got != 3 {
		t.Fatalf("initial replicas = %d, want 3", got)
	}
	eval(t, s, 0, 0, 3, DecisionHold)             // 推荐 0，夹到下限 3
	eval(t, s, 1, 1_000_000, 6, DecisionScaleUp)  // 推荐远超上限，限速到 6
	eval(t, s, 2, 1_000_000, 10, DecisionScaleUp) // min(10,12)=10，触及上限
	eval(t, s, 3, 1_000_000, 10, DecisionHold)    // 不能超过上限
	eval(t, s, 4, 0, 3, DecisionScaleDown)        // W=0、D=0，夹到下限
}

// 创建参数按顺序只报第一个非法项。
func TestNewValidationOrder(t *testing.T) {
	cases := []struct {
		name                   string
		min, max, target, w, d int64
		want                   error
	}{
		{"min below 1", 0, 10, 100, 0, 0, ErrMinReplicas},
		{"max below min", 5, 4, 100, 0, 0, ErrMaxReplicas},
		{"target non-positive", 1, 10, 0, 0, 0, ErrTarget},
		{"window negative", 1, 10, 100, -1, 0, ErrWindow},
		{"cooldown negative", 1, 10, 100, 0, -1, ErrCooldown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.min, tc.max, tc.target, tc.w, tc.d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	_, err := New(0, -5, 0, -1, -1)
	if !errors.Is(err, ErrMinReplicas) {
		t.Fatalf("err = %v, want ErrMinReplicas", err)
	}
}

// 评估时的非法输入按顺序只报第一个，且不改变任何状态。
func TestEvaluateRejectionLeavesStateUntouched(t *testing.T) {
	var logs bytes.Buffer
	s, err := New(1, 100, 100, 1000, 1000, WithLogger(&logs))
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s, 100, 400, 2, DecisionScaleUp)
	eval(t, s, 101, 400, 4, DecisionScaleUp)

	if _, _, err := s.Evaluate(100, 0); !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("err = %v, want ErrTimeRegression", err)
	}
	if _, _, err := s.Evaluate(50, -1); !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("err = %v, want ErrTimeRegression (order)", err)
	}
	if _, _, err := s.Evaluate(101, -1); !errors.Is(err, ErrNegativeMetric) {
		t.Fatalf("err = %v, want ErrNegativeMetric", err)
	}

	if got := s.Replicas(); got != 4 {
		t.Fatalf("replicas = %d, want 4", got)
	}
	if at, ok := s.LastScaleDownAt(); ok {
		t.Fatalf("unexpected last scale down at %d", at)
	}
	// 历史未被污染：101 时刻的高值仍在窗口内压住缩容。
	eval(t, s, 102, 0, 4, DecisionWindowBlocked)
	// now 等于上一次评估时刻是允许的。
	eval(t, s, 102, 0, 4, DecisionWindowBlocked)

	if !bytes.Contains(logs.Bytes(), []byte("reject")) {
		t.Fatalf("logs should contain rejection entries, got:\n%s", logs.String())
	}
}

// 相同序列重放必须得到完全相同的副本数与类别。
func TestDeterministicReplay(t *testing.T) {
	type step struct{ now, metric int64 }
	steps := []step{
		{0, 100}, {1, 250}, {2, 250}, {3, 80}, {4, 0},
		{500, 0}, {1001, 0}, {1002, 300}, {1003, 0},
	}
	type result struct {
		replicas int64
		decision Decision
	}
	run := func() []result {
		s, err := New(2, 50, 100, 1000, 500)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]result, len(steps))
		for i, st := range steps {
			r, d, evalErr := s.Evaluate(st.now, st.metric)
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			out[i] = result{r, d}
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d: %v vs %v", i, first[i], second[i])
		}
	}
}

// 并发评估与查询等价于某个串行顺序，副本数始终在上下限内。
func TestConcurrentAccess(t *testing.T) {
	s, err := New(1, 64, 100, 500, 500)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var clock int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := atomic.AddInt64(&clock, 1)
				metric := int64(((int(now)*37 + id*13) % 400) + 1)
				r, _, evalErr := s.Evaluate(now, metric)
				if evalErr != nil && !errors.Is(evalErr, ErrTimeRegression) {
					t.Errorf("unexpected error: %v", evalErr)
					return
				}
				if evalErr == nil && (r < 1 || r > 64) {
					t.Errorf("replicas %d out of bounds", r)
					return
				}
				if q := s.Replicas(); q < 1 || q > 64 {
					t.Errorf("queried replicas %d out of bounds", q)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// 日志打印输入、输出与判定依据。
func TestLoggerOutput(t *testing.T) {
	var logs bytes.Buffer
	s, err := New(2, 20, 100, 1000, 1000, WithLogger(&logs))
	if err != nil {
		t.Fatal(err)
	}
	eval(t, s, 0, 400, 4, DecisionScaleUp)
	eval(t, s, 1, 0, 4, DecisionWindowBlocked)
	out := logs.String()
	for _, want := range []string{
		"input now=0 metric=400",
		"current=2",
		"clamped=8",
		"output=4",
		"decision=scale_up",
		"reason=scale_up_capped_at_double",
		"decision=window_blocked",
		"reason=window_high_watermark_holds",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q, got:\n%s", want, out)
		}
	}
}
