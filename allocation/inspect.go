package allocation

import "sort"

// ShareView 一条份额的只读快照。
type ShareView struct {
	ID         int64
	TaskID     string
	Semester   string
	TeacherID  string
	Hours      int
	Workload   int
	State      string
	StartWeek  int
	EndWeek    int
	AssignedAt int64
	Deadline   int64
}

// Snapshot 服务当前状态的只读快照，供断言与朴素模型对照。
type Snapshot struct {
	Clock    int64
	Teachers map[string]TeacherView
	Tasks    map[string]TaskSpec
	Shares   []ShareView
	Settled  map[string]*SemesterReport
}

// TeacherView 教师视角：已计入（pending+active）工作量与持有份额。
type TeacherView struct {
	ID      string
	Rank    string
	Holding []int64
	Used    map[string]int
}

func (s *Service) Snapshot() Snapshot {
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	snap := Snapshot{
		Clock:    svc.clock,
		Teachers: map[string]TeacherView{},
		Tasks:    map[string]TaskSpec{},
		Settled:  map[string]*SemesterReport{},
	}
	for id, t := range svc.teachers {
		tv := TeacherView{ID: id, Rank: t.rank, Used: map[string]int{}}
		for k, v := range t.used {
			tv.Used[k] = v
		}
		for id2 := range t.holding {
			tv.Holding = append(tv.Holding, id2)
		}
		sort.Slice(tv.Holding, func(i, j int) bool { return tv.Holding[i] < tv.Holding[j] })
		snap.Teachers[id] = tv
	}
	for id, tk := range svc.tasks {
		snap.Tasks[id] = tk.spec
	}
	ids := make([]int64, 0, len(svc.shares))
	for id := range svc.shares {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		sh := svc.shares[id]
		snap.Shares = append(snap.Shares, ShareView{
			ID: sh.id, TaskID: sh.taskID, Semester: sh.semester,
			TeacherID: sh.teacherID, Hours: sh.hours, Workload: sh.workload,
			State: sh.state, StartWeek: sh.startWeek, EndWeek: sh.endWeek,
			AssignedAt: sh.assignedAt, Deadline: sh.deadline,
		})
	}
	for sem, rep := range svc.settled {
		snap.Settled[sem] = cloneReport(rep)
	}
	return snap
}

// EffectiveUsed 返回某教师在某学期当前计入（pending+active，已扣除释放）的工作量。
func (s *Service) EffectiveUsed(teacherID, semester string) (int, bool) {
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	t, ok := svc.teachers[teacherID]
	if !ok {
		return 0, false
	}
	return t.used[semester], true
}

// IsFrozen 返回学期是否已核算冻结。
func (s *Service) IsFrozen(semester string) bool {
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	_, ok := svc.settled[semester]
	return ok
}
