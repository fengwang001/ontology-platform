package scholarship

import (
	"sort"
	"time"
)

// naiveEvaluate 是按规格独立直接实现的朴素参考模型，仅供测试对照。
// 它不调用引擎的 allocate/processDept/processPool，而是用直白的循环重述规则；
// 与引擎共享的只有纯函数 judge 与名次定义，避免对照双方共享同一处实现缺陷。
func naiveEvaluate(
	levels []LevelConfig,
	deptQuota map[string]map[string]int,
	students map[string]*Student,
	confirmed map[string]Award,
	at time.Time,
) *Result {
	has := map[string]bool{}
	awards := map[string]Award{}
	usedDept := map[string]map[string]int{}
	usedPool := map[string]int{}

	// 已确认奖励原样保留并预占名额（与引擎相同的重评语义）。
	cids := make([]string, 0, len(confirmed))
	for id := range confirmed {
		cids = append(cids, id)
	}
	sort.Strings(cids)
	for _, id := range cids {
		cw := confirmed[id]
		cw.Confirmed = true
		awards[id] = cw
		has[id] = true
		if cw.Source == SourcePool {
			usedPool[cw.Level]++
		} else {
			if usedDept[cw.Level] == nil {
				usedDept[cw.Level] = map[string]int{}
			}
			usedDept[cw.Level][cw.Dept]++
		}
	}

	ranking := map[string][]RankedStudent{}
	eligibleAt := func(lv LevelConfig) []RankedStudent {
		if r, ok := ranking[lv.ID]; ok {
			return r
		}
		var ids []string
		for id, s := range students {
			if judge(s, lv, at).Eligible {
				ids = append(ids, id)
			}
		}
		r := rankStudents(students, ids)
		ranking[lv.ID] = r
		return r
	}

	reflow := map[string]int{}

	// 院系阶段。
	for _, lv := range levels {
		ranked := eligibleAt(lv)
		depts := make([]string, 0)
		for d := range deptQuota[lv.ID] {
			depts = append(depts, d)
		}
		sort.Strings(depts)
		for _, dept := range depts {
			rem := deptQuota[lv.ID][dept]
			if m := usedDept[lv.ID]; m != nil {
				rem -= m[dept]
			}
			var line []RankedStudent
			for _, r := range ranked {
				if r.Dept == dept && !has[r.StudentID] {
					line = append(line, r)
				}
			}
			i := 0
			for i < len(line) {
				j := i + 1
				for j < len(line) && tied(line[i], line[j]) {
					j++
				}
				if j-i <= rem {
					for k := i; k < j; k++ {
						awards[line[k].StudentID] = Award{
							StudentID: line[k].StudentID, Level: lv.ID,
							Dept: dept, Source: SourceDept, Rank: line[k].Rank,
						}
						has[line[k].StudentID] = true
					}
					rem -= j - i
					i = j
					continue
				}
				reflow[lv.ID] += rem
				break
			}
		}
	}

	// 机动池阶段。
	for _, lv := range levels {
		pool := lv.PoolQuota + reflow[lv.ID] - usedPool[lv.ID]
		if pool < 0 {
			pool = 0
		}
		var line []RankedStudent
		for _, r := range ranking[lv.ID] {
			if !has[r.StudentID] {
				line = append(line, r)
			}
		}
		i := 0
		for i < len(line) && pool > 0 {
			j := i + 1
			for j < len(line) && tied(line[i], line[j]) {
				j++
			}
			if j-i <= pool {
				for k := i; k < j; k++ {
					awards[line[k].StudentID] = Award{
						StudentID: line[k].StudentID, Level: lv.ID,
						Dept: line[k].Dept, Source: SourcePool, Rank: line[k].Rank,
					}
					has[line[k].StudentID] = true
				}
				pool -= j - i
				i = j
				continue
			}
			break
		}
	}

	res := &Result{EvaluatedAt: at, Ranking: ranking}
	for _, lv := range levels {
		res.Levels = append(res.Levels, lv.ID)
	}
	// 与引擎相同的确定顺序：等级 -> 来源 -> 院系 -> 名次 -> 学号。
	order := map[string]int{}
	for i, lv := range levels {
		order[lv.ID] = i
	}
	for _, aw := range awards {
		res.Awards = append(res.Awards, aw)
	}
	sort.Slice(res.Awards, func(i, j int) bool {
		x, y := res.Awards[i], res.Awards[j]
		if order[x.Level] != order[y.Level] {
			return order[x.Level] < order[y.Level]
		}
		if x.Source != y.Source {
			return x.Source < y.Source
		}
		if x.Dept != y.Dept {
			return x.Dept < y.Dept
		}
		if x.Rank != y.Rank {
			return x.Rank < y.Rank
		}
		return x.StudentID < y.StudentID
	})
	return res
}
