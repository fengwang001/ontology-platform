package scholarship

import (
	"fmt"
	"sort"
	"time"
)

// allocator 承载一次评定的名额分配过程（院系名额 + 机动池回流）。
//
// 处理顺序严格按规格：
//  1. 已确认奖励先占用其原来源（院系或机动池）名额；
//  2. 院系阶段：等级从高到低，每等级内院系按名称排序逐一处理；
//  3. 机动阶段：所有院系处理完毕后，等级从高到低在全体未获奖者中授予；
//  4. 任一名额边界恰好落在并列组中间时整组跳过，该等级该院系剩余名额全部回流。
type allocator struct {
	eng     *Engine
	at      time.Time
	awarded map[string]bool
	awards  []Award
	reflow  map[string]int // level -> 本院系阶段回流到机动池的名额
}

func (a *allocator) log(step, detail string) {
	a.eng.tracer.Log(step, detail)
}

func (a *allocator) levelIndex(level string) int {
	for i := range a.eng.levels {
		if a.eng.levels[i].ID == level {
			return i
		}
	}
	return -1
}

// run 执行完整分配。confirmed 为保留的已确认奖励（视为预先占用名额）。
func (e *Engine) allocate(at time.Time, confirmed map[string]Award) ([]Award, map[string][]RankedStudent) {
	a := &allocator{eng: e, at: at, awarded: map[string]bool{}}
	a.reflow = map[string]int{}

	// 各等级合格名单与排序（跨院系同一规则），用于机动池与结果快照。
	ranking := map[string][]RankedStudent{}
	elig := map[string]map[string]Eligibility{}
	for li := len(e.levels) - 1; li >= 0; li-- {
		lv := e.levels[li]
		em := map[string]Eligibility{}
		var ids []string
		for id, s := range e.students {
			r := judge(s, lv, at)
			em[id] = r
			if r.Eligible {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		elig[lv.ID] = em
		ranking[lv.ID] = rankStudents(e.students, ids)
		a.log("eligibility", fmt.Sprintf("level=%s eligible=%v", lv.ID, ids))
	}

	// 已确认奖励：保留并预先占用其原来源名额；学生移出候选集。
	usedDept := map[string]map[string]int{} // level -> dept -> used
	usedPool := map[string]int{}
	confirmIDs := make([]string, 0, len(confirmed))
	for id := range confirmed {
		confirmIDs = append(confirmIDs, id)
	}
	sort.Strings(confirmIDs)
	for _, id := range confirmIDs {
		cw := confirmed[id]
		a.awarded[id] = true
		cw.Confirmed = true
		a.awards = append(a.awards, cw)
		if cw.Source == SourcePool {
			usedPool[cw.Level]++
		} else {
			if usedDept[cw.Level] == nil {
				usedDept[cw.Level] = map[string]int{}
			}
			usedDept[cw.Level][cw.Dept]++
		}
		a.log("confirmed-kept", fmt.Sprintf("student=%s level=%s source=%d dept=%s", id, cw.Level, cw.Source, cw.Dept))
	}

	// 院系阶段：等级高 -> 低；院系名称排序保证确定顺序。
	for li := range e.levels {
		lv := e.levels[li]
		deptIDs := make([]string, 0, len(e.deptQuota[lv.ID]))
		for d := range e.deptQuota[lv.ID] {
			deptIDs = append(deptIDs, d)
		}
		sort.Strings(deptIDs)
		for _, dept := range deptIDs {
			a.processDept(lv, dept, ranking[lv.ID], usedDept)
		}
	}

	// 机动阶段：等级高 -> 低；名额 = 配置池名额 - 已确认占用的池名额 + 回流。
	for li := range e.levels {
		lv := e.levels[li]
		pool := a.reflow[lv.ID] + lv.PoolQuota - usedPool[lv.ID]
		if pool < 0 {
			pool = 0
		}
		a.processPool(lv, ranking[lv.ID], pool)
	}

	a.sortAwards()
	return a.awards, ranking
}

func (a *allocator) processDept(lv LevelConfig, dept string, ranked []RankedStudent, used map[string]map[string]int) {
	quota := a.eng.deptQuota[lv.ID][dept]
	usedCount := 0
	if m := used[lv.ID]; m != nil {
		usedCount = m[dept]
	}
	remaining := quota - usedCount

	var indices []int
	for i := range ranked {
		if ranked[i].Dept == dept && !a.awarded[ranked[i].StudentID] {
			indices = append(indices, i)
		}
	}
	granted := map[string]bool{}
	pos := 0
	for pos < len(indices) {
		start := pos
		end := start + 1
		for end < len(indices) && tied(ranked[indices[start]], ranked[indices[end]]) {
			end++
		}
		groupSize := end - start
		if groupSize <= remaining {
			for k := start; k < end; k++ {
				granted[ranked[indices[k]].StudentID] = true
			}
			remaining -= groupSize
			pos = end
			continue
		}
		// 并列组无法整体授予：整组（以及其后所有更低名次者）不授予该等级，
		// 该等级该院系剩余名额全部回流到全局机动池。
		a.reflow[lv.ID] += remaining
		a.log("dept-reflow", fmt.Sprintf("level=%s dept=%s rank=%d groupSize=%d remaining=%d reflow=%d",
			lv.ID, dept, ranked[indices[start]].Rank, groupSize, remaining, a.reflow[lv.ID]))
		remaining = 0
		break
	}

	ids := make([]string, 0, len(granted))
	for id := range granted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a.awarded[id] = true
		var rk int
		for i := range ranked {
			if ranked[i].StudentID == id {
				rk = ranked[i].Rank
				break
			}
		}
		a.awards = append(a.awards, Award{StudentID: id, Level: lv.ID, Dept: dept, Source: SourceDept, Rank: rk})
	}
	a.log("dept-grant", fmt.Sprintf("level=%s dept=%s granted=%v", lv.ID, dept, ids))
}

func (a *allocator) processPool(lv LevelConfig, ranked []RankedStudent, pool int) {
	var indices []int
	for i := range ranked {
		if !a.awarded[ranked[i].StudentID] {
			indices = append(indices, i)
		}
	}
	granted := map[string]Award{}
	pos := 0
	for pos < len(indices) && pool > 0 {
		start := pos
		end := start + 1
		for end < len(indices) && tied(ranked[indices[start]], ranked[indices[end]]) {
			end++
		}
		groupSize := end - start
		if groupSize <= pool {
			for k := start; k < end; k++ {
				r := ranked[indices[k]]
				granted[r.StudentID] = Award{StudentID: r.StudentID, Level: lv.ID, Dept: r.Dept, Source: SourcePool, Rank: r.Rank}
			}
			pool -= groupSize
			pos = end
			continue
		}
		// 机动池同样整组授予或整组不授予；用不完的名额作废（不向低等级流转）。
		a.log("pool-stop", fmt.Sprintf("level=%s rank=%d groupSize=%d pool=%d", lv.ID, ranked[indices[start]].Rank, groupSize, pool))
		break
	}
	ids := make([]string, 0, len(granted))
	for id := range granted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a.awarded[id] = true
		a.awards = append(a.awards, granted[id])
	}
	a.log("pool-grant", fmt.Sprintf("level=%s granted=%v leftover=%d", lv.ID, ids, pool))
}

// sortAwards 使奖励列表的序列化顺序确定：
// 等级次序 -> 来源（院系先于机动）-> 院系 -> 名次 -> 学号。
func (a *allocator) sortAwards() {
	sort.Slice(a.awards, func(i, j int) bool {
		x, y := a.awards[i], a.awards[j]
		if xi, yj := a.levelIndex(x.Level), a.levelIndex(y.Level); xi != yj {
			return xi < yj
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
}
