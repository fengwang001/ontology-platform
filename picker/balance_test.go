package picker

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// chooseFirst 是对照策略：恒取候选集合（按 id 排序）的第一个，
// 同样遵守在途上限与饱和规则。
func chooseFirst(elig []string) string { return elig[0] }

// chooseP2C 用与产品相同的 P2C 公式在朴素状态上决策。
func chooseP2C(elig []string, cost map[string]int64, r1, r2 uint64) string {
	n := len(elig)
	if n == 1 {
		return elig[0]
	}
	i := int(r1 % uint64(n))
	j := int((uint64(i) + 1 + r2%uint64(n-1)) % uint64(n))
	ci, cj := cost[elig[i]], cost[elig[j]]
	if ci < cj || (ci == cj && elig[i] < elig[j]) {
		return elig[i]
	}
	return elig[j]
}

func meanVar(counts []int64) (float64, float64) {
	var sum float64
	for _, c := range counts {
		sum += float64(c)
	}
	mean := sum / float64(len(counts))
	var sq float64
	for _, c := range counts {
		d := float64(c) - mean
		sq += d * d
	}
	return mean, sq / float64(len(counts))
}

// TestP2CBalanceAgainstFirst 用固定种子统计：在“端点有差异的静态代价 +
// 在途惩罚”模型下，P2C 比“恒取首个候选”负载更均衡（方差与最大-最小差更小）。
// 日志打印输入（种子、端点数、轮数）、两种策略输出计数与判定依据。
func TestP2CBalanceAgainstFirst(t *testing.T) {
	const (
		seed      = 99
		endpoints = 10
		rounds    = 20000
		maxInfl   = 1000 // 大 M：只统计选路，不被饱和干扰
	)
	rng := rand.New(rand.NewSource(seed))

	// 每端点基础代价有明显差异；在途惩罚 val*(infl+1) 会自然压低热点。
	base := make(map[string]int64, endpoints)
	ids := make([]string, 0, endpoints)
	for i := 0; i < endpoints; i++ {
		id := string(rune('a' + i))
		ids = append(ids, id)
		base[id] = int64(1 + rng.Intn(100))
	}

	run := func(strategy string) []int64 {
		r := rand.New(rand.NewSource(seed + 1))
		infl := make(map[string]int64, endpoints)
		counts := make([]int64, endpoints)
		for round := 0; round < rounds; round++ {
			elig := make([]string, 0, endpoints)
			cost := make(map[string]int64, endpoints)
			for i, id := range ids {
				if infl[id] < maxInfl {
					elig = append(elig, id)
					cost[id] = base[id] * (infl[id] + 1)
					_ = i
				}
			}
			var winner string
			if strategy == "p2c" {
				winner = chooseP2C(elig, cost, r.Uint64(), r.Uint64())
			} else {
				winner = chooseFirst(elig)
			}
			infl[winner]++
			counts[int(winner[0]-'a')]++
			// 随机释放，在途数随时间衰减。
			if round%2 == 1 {
				for id := range infl {
					if infl[id] > 0 {
						drop := infl[id] / 2
						infl[id] -= drop
					}
				}
			}
		}
		return counts
	}

	p2cCounts := run("p2c")
	firstCounts := run("first")
	p2cMean, p2cVar := meanVar(p2cCounts)
	fMean, fVar := meanVar(firstCounts)

	t.Logf("INPUT seed=%d endpoints=%d rounds=%d base=%v", seed, endpoints, rounds, sortedBase(ids, base))
	t.Logf("P2C   counts=%v mean=%.1f var=%.1f", p2cCounts, p2cMean, p2cVar)
	t.Logf("FIRST counts=%v mean=%.1f var=%.1f", firstCounts, fMean, fVar)

	span := func(c []int64) int64 {
		mn, mx := c[0], c[0]
		for _, v := range c {
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
		}
		return mx - mn
	}
	p2cSpan, fSpan := span(p2cCounts), span(firstCounts)
	t.Logf("DECISION p2cVar(%.1f) < firstVar(%.1f)? %v ; p2cSpan=%d < firstSpan=%d? %v",
		p2cVar, fVar, p2cVar < fVar, p2cSpan, fSpan, p2cSpan < fSpan)

	if !(p2cVar < fVar && p2cSpan < fSpan) {
		t.Fatalf("P2C not more balanced: p2cVar=%v firstVar=%v p2cSpan=%d firstSpan=%d",
			p2cVar, fVar, p2cSpan, fSpan)
	}
	// “恒取首个”在有差异代价下全压同一端点，方差应显著大于 P2C。
	if fVar < 10*p2cVar {
		t.Fatalf("expected first-choice variance >> p2c, got ratio %.2f", fVar/math.Max(p2cVar, 1e-9))
	}
}

func sortedBase(ids []string, base map[string]int64) string {
	s := "map["
	for _, id := range ids {
		s += fmt.Sprintf("%s:%d ", id, base[id])
	}
	return s + "]"
}
