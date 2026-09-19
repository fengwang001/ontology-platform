package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// FindCycles 从 start 出发，仅沿 linkTypes 正向遍历，返回能到达的全部环。
// 每个环是一串 PathStep（首步的 Source 即环的起点，末步的 Target 回到起点）。
// 结果去重并排序，不受 map 遍历顺序影响。
func (s *Store) FindCycles(start ObjectKey, linkTypes []string) [][]PathStep {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lts := dedupeSorted(linkTypes)
	var cycles [][]PathStep
	seen := map[string]bool{}
	var steps []PathStep
	onStack := map[ObjectKey]int{}
	var dfs func(obj ObjectKey)
	dfs = func(obj ObjectKey) {
		onStack[obj] = len(steps)
		for _, lt := range lts {
			for _, tgt := range sortedKeys(s.fwd[lt][obj]) {
				step := PathStep{LinkType: lt, Source: obj, Target: tgt}
				if idx, ok := onStack[tgt]; ok {
					cyc := make([]PathStep, 0, len(steps)-idx+1)
					cyc = append(cyc, steps[idx:]...)
					cyc = append(cyc, step)
					canon, key := canonicalCycle(cyc)
					if !seen[key] {
						seen[key] = true
						cycles = append(cycles, canon)
					}
					continue
				}
				steps = append(steps, step)
				dfs(tgt)
				steps = steps[:len(steps)-1]
			}
		}
		delete(onStack, obj)
	}
	dfs(start)
	sort.Slice(cycles, func(i, j int) bool {
		return cycleKey(cycles[i]) < cycleKey(cycles[j])
	})
	return cycles
}

func dedupeSorted(in []string) []string {
	set := map[string]bool{}
	var out []string
	for _, s := range in {
		if !set[s] {
			set[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func stepString(st PathStep) string {
	return fmt.Sprintf("%s|%s/%s|%s/%s", st.LinkType, st.Source.Type, st.Source.ID, st.Target.Type, st.Target.ID)
}

func cycleKey(cyc []PathStep) string {
	parts := make([]string, len(cyc))
	for i, st := range cyc {
		parts[i] = stepString(st)
	}
	return strings.Join(parts, ">")
}

// canonicalCycle 将环旋转到字典序最小的起点，保证同一环的表示唯一。
func canonicalCycle(cyc []PathStep) ([]PathStep, string) {
	best := -1
	bestKey := ""
	for i := range cyc {
		rot := make([]PathStep, 0, len(cyc))
		rot = append(rot, cyc[i:]...)
		rot = append(rot, cyc[:i]...)
		key := cycleKey(rot)
		if best == -1 || key < bestKey {
			best = i
			bestKey = key
		}
	}
	canon := make([]PathStep, 0, len(cyc))
	canon = append(canon, cyc[best:]...)
	canon = append(canon, cyc[:best]...)
	return canon, bestKey
}
