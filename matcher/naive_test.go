package matcher

// naiveLev 直接计算两个字节串的字节级 Levenshtein 距离。
func naiveLev(a, b []byte) int {
	la, lb := len(a), len(b)
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// naiveResult 是朴素实现的输出。
type naiveResult struct {
	reports    []Match
	suppressed int
	consumed   int
}

// naiveRun 对整段文本一次性计算：枚举每个结束位置 e 与每个起点 s，
// 取最小距离与最左起点，再按连续命中段折叠代表并应用抑制规则。
func naiveRun(pattern []byte, k int, text []byte) naiveResult {
	type hit struct {
		e, dist, start int
	}
	var hits []hit
	for e := 1; e <= len(text); e++ {
		bestDist := len(pattern) + 1
		bestStart := -1
		for s := 0; s <= e; s++ {
			d := naiveLev(pattern, text[s:e])
			if d < bestDist || (d == bestDist && s < bestStart) {
				bestDist = d
				bestStart = s
			}
		}
		if bestDist <= k {
			hits = append(hits, hit{e, bestDist, bestStart})
		}
	}

	var reports []Match
	suppressed := 0
	lastEnd := 0
	flush := func(seg []hit) {
		rep := seg[0]
		for _, h := range seg[1:] {
			if h.dist < rep.dist {
				rep = h
			}
		}
		if rep.start < lastEnd {
			suppressed++
			return
		}
		reports = append(reports, Match{End: rep.e, Dist: rep.dist, Start: rep.start})
		if rep.e > lastEnd {
			lastEnd = rep.e
		}
	}

	for i := 0; i < len(hits); {
		j := i + 1
		for j < len(hits) && hits[j].e == hits[j-1].e+1 {
			j++
		}
		flush(hits[i:j])
		i = j
	}

	return naiveResult{reports: reports, suppressed: suppressed, consumed: len(text)}
}
