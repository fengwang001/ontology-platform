package snippets

import (
	"fmt"
	"sort"
)

// naiveSnippets 是完全按照题目文字逐轮写出的朴素参考实现，
// 用于与生产实现 selectSnippets 对拍。
func naiveSnippets(hits []Hit, n, W, K int) []Snippet {
	// 按完全相同的 (s,e) 去重；顺序无关，朴素实现里也排序固定下来。
	seen := map[Hit]bool{}
	cur := []Hit{}
	for _, h := range hits {
		if !seen[h] {
			seen[h] = true
			cur = append(cur, h)
		}
	}
	sort.Slice(cur, func(i, j int) bool {
		if cur[i].Start != cur[j].Start {
			return cur[i].Start < cur[j].Start
		}
		return cur[i].End < cur[j].End
	})

	type chosen struct{ a, E int }
	selected := []chosen{}
	type round struct {
		a, E int
		c    []Hit
	}
	rounds := []round{}

	for len(selected) < K {
		// 每轮取可用命中：与全部已选片段范围都不相交。
		avail := []Hit{}
		for _, h := range cur {
			ok := true
			for _, s := range selected {
				if h.Start < s.E && s.a < h.End {
					ok = false
					break
				}
			}
			if ok {
				avail = append(avail, h)
			}
		}
		if len(avail) == 0 {
			break
		}

		// 每个不同起点 a 评估一次。
		starts := map[int]bool{}
		for _, h := range avail {
			starts[h.Start] = true
		}
		as := []int{}
		for a := range starts {
			as = append(as, a)
		}
		sort.Ints(as)

		bestA, bestE, bestScore := -1, -1, -1
		var bestC []Hit
		for _, a := range as {
			r := a + W
			if r > n {
				r = n
			}
			for _, s := range selected {
				if s.a > a && s.a < r {
					r = s.a
				}
			}
			cset := []Hit{}
			E := -1
			for _, h := range avail {
				if h.Start >= a && h.End <= r {
					cset = append(cset, h)
					if h.End > E {
						E = h.End
					}
				}
			}
			score := len(cset)
			if score > bestScore {
				bestScore, bestA, bestE, bestC = score, a, E, cset
			}
		}
		if bestScore == 0 {
			break
		}
		selected = append(selected, chosen{bestA, bestE})
		rounds = append(rounds, round{bestA, bestE, bestC})
	}

	// 输出按 Start 升序，而非选取顺序。
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].a < rounds[j].a })
	out := []Snippet{}
	for _, rd := range rounds {
		sort.Slice(rd.c, func(i, j int) bool {
			if rd.c[i].Start != rd.c[j].Start {
				return rd.c[i].Start < rd.c[j].Start
			}
			return rd.c[i].End < rd.c[j].End
		})
		hl := []Range{}
		for _, h := range rd.c {
			if len(hl) == 0 || h.Start >= hl[len(hl)-1].End {
				hl = append(hl, Range{h.Start, h.End})
			} else if h.End > hl[len(hl)-1].End {
				hl[len(hl)-1].End = h.End
			}
		}
		out = append(out, Snippet{Start: rd.a, End: rd.E, Highlights: hl, Score: len(rd.c)})
	}
	return out
}

func formatSnippets(ss []Snippet) string {
	return fmt.Sprintf("%v", ss)
}
