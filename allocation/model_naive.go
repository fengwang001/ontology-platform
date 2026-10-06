package allocation

// naiveModel 独立编写的朴素对照模型。
// 正式服务用位图/引用计数做 O(节次数) 判定；朴素模型保留份额列表，
// 每次判定都对该教师全部历史份额线性扫描（开销随历史任务数增长）。
// 同一随机操作序列上两者的错误类别与最终份额集合必须一致。
type naiveModel struct {
	cfg       Config
	clock     int64
	ranks     map[string]RankLimit
	teachers  map[string]string
	tasks     map[string]TaskSpec
	shares    []naiveShare
	nextID    int64
	settled   map[string]map[string]naiveTeacherResult
	settledAt map[string]int64
}

type naiveShare struct {
	id                   int64
	taskID, teacherID    string
	hours                int
	state                string
	startWeek, endWeek   int
	assignedAt, deadline int64
}

type naiveTeacherResult struct {
	rank                                        string
	total, min, max                             int
	deficit, excess, brought, used, unmet, next int
}

func newNaiveModel(cfg Config) *naiveModel {
	ranks := map[string]RankLimit{}
	for _, r := range cfg.Ranks {
		ranks[r.Rank] = r
	}
	return &naiveModel{
		cfg: cfg, ranks: ranks,
		teachers:  map[string]string{},
		tasks:     map[string]TaskSpec{},
		settled:   map[string]map[string]naiveTeacherResult{},
		settledAt: map[string]int64{},
	}
}

func (m *naiveModel) fail(code ErrorCode, idx int) *Error {
	return newErrAt(code, idx, "naive rejection")
}

func (m *naiveModel) checkClock(now int64) *Error {
	if now < m.clock {
		return m.fail(ErrClockRollback, -1)
	}
	return nil
}

func (m *naiveModel) expireAll(now int64) {
	for i := range m.shares {
		if m.shares[i].state == statePending && now > m.shares[i].deadline {
			m.shares[i].state = stateReleased
		}
	}
}

// expireTeacher 只释放某教师名下已超时的待确认份额，
// 与正式服务"由后续操作触达"的惰性范围保持一致。
func (m *naiveModel) expireTeacher(teacherID string, now int64) {
	for i := range m.shares {
		sh := &m.shares[i]
		if sh.teacherID == teacherID && sh.state == statePending && now > sh.deadline {
			sh.state = stateReleased
		}
	}
}

func weeksOverlap(a1, a2, b1, b2 int) bool { return a1 <= b2 && b1 <= a2 }

func periodsOverlap(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
