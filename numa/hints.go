package numa

import "math/bits"

const maxCombinations = 100000

func popcount(m uint64) int { return bits.OnesCount64(m) }

// builtinHints 依据当前各节点空闲量生成内置 cpu 提供者的提示。
// 对每个可行（掩码内空闲和 >= req）非空掩码产出提示；preferred 取决于
// 其位数是否等于所有可行掩码中的最小位数 Zc。
func builtinHints(n int, free []int64, req int64) []Hint {
	var sums [256]int64
	for m := 1; m < 1<<uint(n); m++ {
		var s int64
		for i := 0; i < n; i++ {
			if m&(1<<uint(i)) != 0 {
				s += free[i]
			}
		}
		sums[m] = s
	}
	zc := n + 1
	for m := 1; m < 1<<uint(n); m++ {
		if sums[m] >= req {
			if c := popcount(uint64(m)); c < zc {
				zc = c
			}
		}
	}
	if zc == n+1 {
		return nil
	}
	hints := make([]Hint, 0, (1<<uint(n))-1)
	for m := 1; m < 1<<uint(n); m++ {
		if sums[m] >= req {
			hints = append(hints, Hint{Mask: uint64(m), Preferred: popcount(uint64(m)) == zc})
		}
	}
	return hints
}

// normalizeProvider 对单个提供者的原始回答做归一。
// 无偏好 -> [(全掩码, true)]；空列表 -> [(全掩码, false)]；
// single 策略下非空列表先丢弃位数不为 1 的提示，丢弃后为空按空列表处理。
func normalizeProvider(p ProviderHints, full uint64, single bool) []Hint {
	if p.NoPreference {
		return []Hint{{Mask: full, Preferred: true}}
	}
	in := p.Hints
	if single && len(in) > 0 {
		filtered := make([]Hint, 0, len(in))
		for _, h := range in {
			if popcount(h.Mask) == 1 {
				filtered = append(filtered, h)
			}
		}
		in = filtered
	}
	if len(in) == 0 {
		return []Hint{{Mask: full, Preferred: false}}
	}
	out := make([]Hint, len(in))
	copy(out, in)
	return out
}

// mergeHints 枚举各提供者各取一个提示的全部组合，掩码按位与，
// 丢弃空掩码，再按 allPref 候选的最小位数 Z 做相对窄化。
func mergeHints(lists [][]Hint) []Hint {
	type cand struct {
		mask    uint64
		allPref bool
	}
	cands := make([]cand, 0, maxCombinations)

	idx := make([]int, len(lists))
	for {
		mask := ^uint64(0)
		allPref := true
		for i, h := range lists {
			mask &= h[idx[i]].Mask
			allPref = allPref && h[idx[i]].Preferred
		}
		if mask != 0 {
			cands = append(cands, cand{mask, allPref})
		}
		i := len(lists) - 1
		for i >= 0 {
			idx[i]++
			if idx[i] < len(lists[i]) {
				break
			}
			idx[i] = 0
			i--
		}
		if i < 0 {
			break
		}
	}

	z := 0
	for _, c := range cands {
		if c.allPref {
			if b := popcount(c.mask); z == 0 || b < z {
				z = b
			}
		}
	}

	out := make([]Hint, 0, len(cands))
	for _, c := range cands {
		out = append(out, Hint{
			Mask:      c.mask,
			Preferred: c.allPref && z != 0 && popcount(c.mask) == z,
		})
	}
	return out
}

// bestHint 从候选中选出最优提示：preferred 优先，位数少优先，掩码数值小优先。
// 没有候选时返回 (full, false)。
func bestHint(cands []Hint, full uint64) Hint {
	best := Hint{Mask: full, Preferred: false}
	found := false
	for _, c := range cands {
		if !found || betterHint(c, best) {
			best = c
			found = true
		}
	}
	return best
}

func betterHint(a, b Hint) bool {
	if a.Preferred != b.Preferred {
		return a.Preferred
	}
	ca, cb := popcount(a.Mask), popcount(b.Mask)
	if ca != cb {
		return ca < cb
	}
	return a.Mask < b.Mask
}
