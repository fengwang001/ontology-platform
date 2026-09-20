package ontology

import (
	"math"
	"testing"
)

// 权重语义统计核对：k=1 时，权重为 w 的元素被选中概率应正比于 w。
//
// 方法：三个元素权重分别为 1、2、3，期望入选概率为 1/6、2/6、3/6。
// 用固定种子跑 rounds=2000 轮（每轮种子由轮次确定性地推出），
// 统计各元素入选频率并与期望比例比较。
//
// 容差推导：频率是伯努利估计，标准差 sigma = sqrt(p(1-p)/n)，
// 在 p<=1/2、n=2000 时 sigma <= sqrt(0.25/2000) 约等于 0.0112。
// 取 tol = 0.05，约为 4.5 倍最大标准差，足以区分"比例正确"与
// "均匀抽样"（均匀时偏差达 1/6 约 0.167，远超容差）。
// 由于种子完全固定，本测试是确定性的，跑两次结论必然相同。
func TestSelectionFrequencyProportionalToWeight(t *testing.T) {
	const rounds = 2000
	const tol = 0.05
	weights := []float64{1, 2, 3}
	totalW := 6.0
	counts := make([]int, len(weights))
	for round := 0; round < rounds; round++ {
		seed := uint64(0xC0FFEE) + uint64(round)*0x9E3779B1
		s, err := NewSampler(1, seed)
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range weights {
			if err := s.Add(i, w); err != nil {
				t.Fatal(err)
			}
		}
		got := s.Sample()
		if len(got) != 1 {
			t.Fatalf("round %d: expected 1 sample, got %d", round, len(got))
		}
		counts[got[0].(int)]++
	}
	for i, w := range weights {
		freq := float64(counts[i]) / rounds
		want := w / totalW
		if math.Abs(freq-want) > tol {
			t.Fatalf("weight %v: freq %.4f, want %.4f ± %.2f",
				w, freq, want, tol)
		}
		t.Logf("weight %v: freq %.4f, want %.4f", w, freq, want)
	}
}

// 容量 k>1 时的比例核对：在淘汰压力下，
// 重权重元素的留存率必须显著高于轻权重元素。
func TestHeavierWeightRetainedMore(t *testing.T) {
	const rounds = 2000
	light, heavy := 0, 0
	for round := 0; round < rounds; round++ {
		s, err := NewSampler(2, uint64(12345)+uint64(round))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Add("light", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.Add("heavy", 3); err != nil {
			t.Fatal(err)
		}
		// 加入 8 个权重为 1 的干扰元素，制造淘汰压力。
		for i := 0; i < 8; i++ {
			if err := s.Add(i, 1); err != nil {
				t.Fatal(err)
			}
		}
		for _, it := range s.Sample() {
			switch it {
			case "light":
				light++
			case "heavy":
				heavy++
			}
		}
	}
	if heavy <= light {
		t.Fatalf("heavy retained %d times, light %d times", heavy, light)
	}
	t.Logf("heavy=%d light=%d", heavy, light)
}
