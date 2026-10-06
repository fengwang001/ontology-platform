package toollife

import (
	"fmt"
	"testing"
	"time"
)

// primeHistory 在组内制造 settleHistory 笔已结算申请（历史台账不断膨胀），
// 但所有刀最终状态一致：T0 恰好空、其余刀预占占满，保证选刀仍只扫描到 T0 即命中。
func primeHistory(t *testing.T, s *Service, tools int, settleHistory int) {
	t.Helper()
	ids := make([]string, tools)
	for i := range ids {
		ids[i] = fmt.Sprintf("T%d", i)
	}
	cfg := GroupConfig{LifeLimit: 1000, WarnPermille: 1000, Mode: Strict, ToolIDs: ids}
	if err := s.AddGroup("G", cfg); err != nil {
		t.Fatal(err)
	}
	// 在 T0 上反复申请 1、记账 1 settleHistory 次：used 不增反增会耗尽。
	// 为保持「刀具状态相同且有容量」，改为申请后立即中止（也进入历史台账），
	// 台账规模随历史线性增长，而 used/reserved 恒为 0。
	for i := 0; i < settleHistory; i++ {
		req := fmt.Sprintf("hist-%d", i)
		if _, err := s.Apply("G", req, 1); err != nil {
			t.Fatalf("history apply: %v", err)
		}
		if err := s.Cancel(req); err != nil {
			t.Fatalf("history cancel: %v", err)
		}
	}
}

// historyScale 两档历史规模。
const (
	smallHistory = 100
	largeHistory = 100_000
)

// applyUnderHistory 在给定历史规模下完成 n 次「申请+中止」，返回总耗时。
func applyUnderHistory(b *testing.B, tools, history, n int) {
	s := New()
	primeHistoryB(b, s, tools, history)
	b.ResetTimer()
	for i := 0; i < n; i++ {
		req := fmt.Sprintf("live-%d", i)
		if _, err := s.Apply("G", req, 1); err != nil {
			b.Fatal(err)
		}
		if err := s.Cancel(req); err != nil {
			b.Fatal(err)
		}
	}
}

func primeHistoryB(b *testing.B, s *Service, tools, settleHistory int) {
	ids := make([]string, tools)
	for i := range ids {
		ids[i] = fmt.Sprintf("T%d", i)
	}
	if err := s.AddGroup("G", GroupConfig{LifeLimit: 1000, WarnPermille: 1000, Mode: Strict, ToolIDs: ids}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < settleHistory; i++ {
		req := fmt.Sprintf("hist-%d", i)
		if _, err := s.Apply("G", req, 1); err != nil {
			b.Fatal(err)
		}
		if err := s.Cancel(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyHistory100(b *testing.B) {
	applyUnderHistory(b, 8, smallHistory, b.N)
}

func BenchmarkApplyHistory100k(b *testing.B) {
	applyUnderHistory(b, 8, largeHistory, b.N)
}

// TestHistoryScaleIndependent 可验证地证明选刀开销不随历史申请总数增长：
// 同一刀组、同一刀具数，历史台账相差 1000 倍时，单次申请平均耗时的比值
// 不得超过 2.5（允许 GC/噪声余量；若选刀遍历历史，耗时将线性增长数百倍）。
func TestHistoryScaleIndependent(t *testing.T) {
	const tools, liveN = 8, 2000
	measure := func(history int) float64 {
		s := New()
		primeHistory(t, s, tools, history)
		start := time.Now().UnixNano()
		for i := 0; i < liveN; i++ {
			req := fmt.Sprintf("live-%d", i)
			if _, err := s.Apply("G", req, 1); err != nil {
				t.Fatal(err)
			}
			if err := s.Cancel(req); err != nil {
				t.Fatal(err)
			}
		}
		elapsed := time.Now().UnixNano() - start
		return float64(elapsed) / float64(liveN)
	}
	small := measure(smallHistory)
	large := measure(largeHistory)
	ratio := large / small
	t.Logf("输入: 刀具数=%d 固定；历史申请 %d vs %d（相差1000倍）\n",
		tools, smallHistory, largeHistory)
	t.Logf("输出: 单次申请+中止平均耗时 small=%.1fns large=%.1fns 比值=%.2f\n", small, large, ratio)
	t.Logf("判定依据: 选刀仅线性扫描组内 %d 把刀（canCarry O(1)），申请台账按编号哈希存取，O(1)，不参与选刀；", tools)
	t.Logf("         若实现遍历历史台账，比值应接近 1000；实测比值 < 2.5 即证明与历史长度无关。")
	if ratio >= 2.5 {
		t.Fatalf("单次操作耗时随历史增长过多: 比值 %.2f >= 2.5", ratio)
	}

	// 反向对照：刀具数扩大 100 倍时，选刀耗时应可观测增长（证明扫描的是刀具而非历史）。
	scan := func(tools int) float64 {
		s := New()
		primeHistory(t, s, tools, 0)
		// 把前 tools-1 把刀用预占占满，使选刀必须扫到最后一把。
		for i := 0; i < tools-1; i++ {
			if _, err := s.Apply("G", fmt.Sprintf("block-%d", i), 1000); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now().UnixNano()
		const n = 500
		for i := 0; i < n; i++ {
			// 必失败，且为暂无余量（所有刀：前 tools-1 预占占满，末把可用但 est=1000 能承载一次）
			if i == 0 {
				r, err := s.Apply("G", "tail", 1000)
				if err != nil || r.ToolID != fmt.Sprintf("T%d", tools-1) {
					t.Fatalf("应选中末把刀: %+v %v", r, err)
				}
				continue
			}
			if _, err := s.Apply("G", fmt.Sprintf("miss-%d", i), 1); CodeOf(err) != ErrNoMargin {
				t.Fatalf("应暂无余量: %v", err)
			}
		}
		return float64(time.Now().UnixNano()-start) / float64(n)
	}
	fast := scan(8)
	slow := scan(800)
	t.Logf("对照: 刀具数 8 -> 800（全扫描失败路径），平均耗时 %.1fns -> %.1fns，比值 %.2f（应随刀具数增长）",
		fast, slow, slow/fast)
	if slow <= fast*2 {
		t.Fatalf("刀具数扩大100倍应带来可观测的扫描开销增长")
	}
}
