package scholarship

import (
	"sort"
	"time"
)

// runAllocation 用当前数据从零完成一次完整评定。
// confirmed 中的已确认奖励被钉住并优先占用名额(先占院系名额, 不足再占机动池);
// 除此之外不读取任何历史评定状态, 因此重评开销与历史评定次数无关。
func runAllocation(cfg *Config, students map[string]Student, confirmed map[string]Award, at time.Time, stats *Stats) *Result {
	levels := cfg.Levels
	levelIdx := make(map[string]int, len(levels))
	for i, lv := range levels {
		levelIdx[lv.ID] = i
	}

	deptSet := map[string]bool{}
	for _, lv := range levels {
		for d := range lv.Quotas {
			deptSet[d] = true
		}
	}
	depts := sortedKeys(deptSet)

	all := make([]*Student, 0, len(students))
	for _, id := range sortedKeys(students) {
		s := students[id]
		all = append(all, &s)
		stats.StudentReads++
	}

	// 资格判定: 每学生每等级各一次, 总量 O(学生数 x 等级数)。
	elig := make(map[string][]FailReason, len(all))
	for _, s := range all {
		reasons := make([]FailReason, len(levels))
		for i := range levels {
			reasons[i] = checkEligibility(s, &levels[i], at)
		}
		elig[s.ID] = reasons
	}

	remaining := make([]map[string]int, len(levels))
	for i, lv := range levels {
		remaining[i] = make(map[string]int, len(lv.Quotas))
		for d, q := range lv.Quotas {
			remaining[i][d] = q
		}
	}
	pool := cfg.PoolSize
	awarded := make(map[string]Award, len(confirmed))

	// 已确认奖励钉住: 保留原等级, 先占本院系名额, 院系名额不足时占机动池。
	for _, id := range sortedKeys(confirmed) {
		a := confirmed[id]
		s, ok := students[id]
		if !ok {
			continue
		}
		stats.StudentReads++
		li := levelIdx[a.LevelID]
		a.DeptID = s.DeptID
		if remaining[li][s.DeptID] > 0 {
			remaining[li][s.DeptID]--
			a.FromPool = false
		} else {
			pool--
			a.FromPool = true
		}
		awarded[id] = a
	}

	byDept := map[string][]*Student{}
	for _, s := range all {
		byDept[s.DeptID] = append(byDept[s.DeptID], s)
	}

	// 名次表: 院系内全体学生按排序键排序, 并列同名次跳号。
	rankings := make([]DeptRanking, 0, len(depts))
	for _, d := range depts {
		sorted := append([]*Student(nil), byDept[d]...)
		sortStudents(sorted)
		rankings = append(rankings, DeptRanking{DeptID: d, Entries: rankEntries(sorted)})
		stats.StudentReads += int64(len(sorted))
	}

	// 院系阶段: 等级从高到低, 院系逐一处理。
	for li := range levels {
		for _, d := range depts {
			rem := remaining[li][d]
			if rem <= 0 {
				continue
			}
			var cands []*Student
			for _, s := range byDept[d] {
				if _, ok := awarded[s.ID]; ok {
					continue
				}
				if elig[s.ID][li] != ReasonNone {
					continue
				}
				cands = append(cands, s)
			}
			stats.StudentReads += int64(len(byDept[d]))
			sortStudents(cands)
			for _, g := range tieGroups(cands) {
				if len(g) <= rem {
					for _, s := range g {
						awarded[s.ID] = Award{StudentID: s.ID, LevelID: levels[li].ID, DeptID: d}
					}
					rem -= len(g)
				} else {
					// 剩余名额放不下整个并列组: 整组转入下一等级, 剩余名额回流机动池。
					pool += rem
					rem = 0
					break
				}
			}
			remaining[li][d] = rem
		}
	}

	// 机动池阶段: 全部院系处理完毕后, 按等级从高到低跨院系授予;
	// 并列组整组授予或整组跳过, 用不完的名额作废。
	for li := range levels {
		if pool <= 0 {
			break
		}
		var cands []*Student
		for _, s := range all {
			if _, ok := awarded[s.ID]; ok {
				continue
			}
			if elig[s.ID][li] != ReasonNone {
				continue
			}
			cands = append(cands, s)
		}
		stats.StudentReads += int64(len(all))
		sortStudents(cands)
		for _, g := range tieGroups(cands) {
			if len(g) <= pool {
				for _, s := range g {
					awarded[s.ID] = Award{StudentID: s.ID, LevelID: levels[li].ID, DeptID: s.DeptID, FromPool: true}
				}
				pool -= len(g)
			}
		}
	}

	awards := make([]Award, 0, len(awarded))
	for _, id := range sortedKeys(awarded) {
		awards = append(awards, awarded[id])
	}
	sort.Slice(awards, func(i, j int) bool {
		li, lj := levelIdx[awards[i].LevelID], levelIdx[awards[j].LevelID]
		if li != lj {
			return li < lj
		}
		if awards[i].DeptID != awards[j].DeptID {
			return awards[i].DeptID < awards[j].DeptID
		}
		return awards[i].StudentID < awards[j].StudentID
	})

	dq := []Disqualification{}
	for _, id := range sortedKeys(students) {
		for li := range levels {
			if r := elig[id][li]; r != ReasonNone {
				dq = append(dq, Disqualification{StudentID: id, LevelID: levels[li].ID, Reason: r})
			}
		}
	}

	return &Result{Awards: awards, Disqualified: dq, Rankings: rankings, PoolLeft: pool}
}
