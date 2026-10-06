package allocation

import (
	"sort"
	"sync"
)

const (
	statePending  = "pending"
	stateActive   = "active"
	stateRejected = "rejected"
	stateReleased = "released"
)

type task struct {
	spec     TaskSpec
	scalePct int
	workload int // 满学时单人折算（仅用于校验/展示）
}

type teacher struct {
	id    string
	rank  string
	sched *schedule
	// 当前计入占用与上限的份额 ID（pending + active）
	holding map[int64]bool
	// 学期 -> 累计折算工作量（pending + active）
	used map[string]int
}

type share struct {
	id         int64
	taskID     string
	semester   string
	teacherID  string
	hours      int
	workload   int // 按该份额自身学时独立折算
	state      string
	startWeek  int
	endWeek    int
	assignedAt int64
	deadline   int64
}

// internalService 保存服务全部可变状态。
type internalService struct {
	mu       sync.Mutex
	cfg      Config
	clock    int64
	teachers map[string]*teacher
	tasks    map[string]*task
	shares   map[int64]*share
	nextID   int64
	settled  map[string]*SemesterReport // 已核算（冻结）学期
	tracer   Tracer
}

// splitHours 把 hours 学时均匀分布到 (end-start+1) 周：
// 商为每周基础学时，余数 r 个零头学时归起始最早的 r 周。
// 给定子区间 [a,b]（任务周次坐标内），返回该区间分到的学时。
// 若 extraBefore 为真，表示该子区间恰好覆盖前若干个零头周。
func splitHours(totalHours, startWeek, endWeek, a, b int) int {
	weeks := endWeek - startWeek + 1
	base := totalHours / weeks
	rem := totalHours % weeks
	var h int
	for w := a; w <= b; w++ {
		h += base
		idx := w - startWeek
		if idx < rem {
			h++
		}
	}
	return h
}

func (svc *internalService) trace(format string, args ...any) {
	if svc.tracer != nil {
		svc.tracer.Logf(format, args...)
	}
}

func (svc *internalService) rankLimit(rank string) (RankLimit, bool) {
	for _, r := range svc.cfg.Ranks {
		if r.Rank == rank {
			return r, true
		}
	}
	return RankLimit{}, false
}

// expire 惰性释放某教师名下已超时的待确认份额。
// 恰等于 deadline 仍有效（now > deadline 才超时）。
func (svc *internalService) expire(t *teacher, now int64) {
	var stale []int64
	for id := range t.holding {
		sh := svc.shares[id]
		if sh.state == statePending && now > sh.deadline {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		svc.releaseShare(svc.shares[id], "lazy-expire")
	}
}

func (svc *internalService) releaseShare(sh *share, reason string) {
	if sh.state != statePending {
		return
	}
	t := svc.teachers[sh.teacherID]
	tk := svc.tasks[sh.taskID]
	t.sched.remove(sh.startWeek, sh.endWeek, tk.spec.Periods)
	delete(t.holding, sh.id)
	t.used[sh.semester] -= sh.workload
	sh.state = stateReleased
	svc.trace("release share=%d reason=%s", sh.id, reason)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
