package claim

import (
	"sort"
	"testing"
)

// 朴素逐单位余数法（与 internal/naive 的实现同思路，独立书写于本包内）。
func shareByUnitSteps(candidates []allocationCandidate, pot int64) map[string]int64 {
	out := map[string]int64{}
	var sum int64
	for _, c := range candidates {
		sum += c.Capacity
	}
	if sum <= 0 || pot <= 0 {
		for _, c := range candidates {
			out[c.PolicyID] = 0
		}
		return out
	}
	if pot > sum {
		pot = sum
	}
	order := append([]allocationCandidate(nil), candidates...)
	sort.Slice(order, func(i, j int) bool {
		if order[i].StartDay != order[j].StartDay {
			return order[i].StartDay < order[j].StartDay
		}
		return order[i].PolicyID < order[j].PolicyID
	})
	var given int64
	for _, c := range order {
		q := pot * c.Capacity / sum
		if q > c.Capacity {
			q = c.Capacity
		}
		out[c.PolicyID] = q
		given += q
	}
	rem := pot - given
	for rem > 0 {
		progress := false
		for _, c := range order {
			if rem <= 0 {
				break
			}
			if out[c.PolicyID] < c.Capacity {
				out[c.PolicyID]++
				rem--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return out
}

// shareGroup 必须与“逐单位循环补位”的朴素实现完全一致，包括容量不足者被
// 跳过、余数循环回到开头等情形。
func TestShareGroupMatchesUnitSteps(t *testing.T) {
	var seed uint64 = 0xabcdef123456
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	for iter := 0; iter < 2000; iter++ {
		n := 1 + int(next()%8)
		cs := make([]allocationCandidate, n)
		for i := range cs {
			cs[i] = allocationCandidate{
				PolicyID: string(rune('a' + i)),
				StartDay: int(next() % 4),
				Capacity: int64(next() % 12),
			}
		}
		pot := int64(next() % 40)
		got := shareGroup(cs, pot)
		want := shareByUnitSteps(cs, pot)
		for _, c := range cs {
			if got[c.PolicyID] != want[c.PolicyID] {
				t.Fatalf("iter %d cs=%+v pot=%d: got=%v want=%v", iter, cs, pot, got, want)
			}
			if got[c.PolicyID] > c.Capacity {
				t.Fatalf("share exceeds capacity")
			}
		}
	}
}

func BenchmarkShareGroup(b *testing.B) {
	cs := make([]allocationCandidate, 256)
	for i := range cs {
		cs[i] = allocationCandidate{
			PolicyID: string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/676)),
			StartDay: i % 7,
			Capacity: int64(1 + i%1000),
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = shareGroup(cs, 100000)
	}
}
