package teaching

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// svcSnapshot 从生产服务提取与朴素模型一致的完整状态。
func svcSnapshot(t *testing.T, s *Service) naiveSnapshot {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepExpiredLocked()
	snap := naiveSnapshot{
		now:     s.now,
		loads:   map[string]int{},
		allocs:  map[string]int{},
		settled: map[string]SettlementResult{},
		credits: map[string]int{},
	}
	keys := make([]string, 0, len(s.assignments))
	for k := range s.assignments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := s.assignments[k]
		snap.items = append(snap.items, AssignmentView{
			TeacherID: a.teacherID,
			TaskID:    a.taskID,
			Hours:     a.hours,
			Status:    a.status,
			Deadline:  a.deadline,
			WeekStart: a.weekStart,
			WeekEnd:   a.weekEnd,
		})
	}
	for tid := range s.teachers {
		snap.loads[tid] = s.load[tid]
	}
	for tid, n := range s.allocated {
		snap.allocs[tid] = n
	}
	for k, v := range s.settlements {
		snap.settled[k] = *v
	}
	for k, v := range s.credits {
		snap.credits[k] = v
	}
	return snap
}

func cmpSnapshot(a, b naiveSnapshot) string {
	if a.now != b.now {
		return fmt.Sprintf("now %d != %d", a.now, b.now)
	}
	if fmt.Sprint(a.items) != fmt.Sprint(b.items) {
		return fmt.Sprintf("items differ:\n%v\nvs\n%v", a.items, b.items)
	}
	if fmt.Sprint(a.loads) != fmt.Sprint(b.loads) {
		return fmt.Sprintf("loads differ: %v vs %v", a.loads, b.loads)
	}
	if fmt.Sprint(a.allocs) != fmt.Sprint(b.allocs) {
		return fmt.Sprintf("allocs differ: %v vs %v", a.allocs, b.allocs)
	}
	if fmt.Sprint(a.settled) != fmt.Sprint(b.settled) {
		return fmt.Sprintf("settlements differ: %v vs %v", a.settled, b.settled)
	}
	if fmt.Sprint(a.credits) != fmt.Sprint(b.credits) {
		return fmt.Sprintf("credits differ: %v vs %v", a.credits, b.credits)
	}
	return ""
}

func errCodeIndex(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		if e == nil {
			return "ok"
		}
		return fmt.Sprintf("ERR code=%d index=%d (%s)", e.Code, e.Index, e.Msg)
	}
	return "ERR " + err.Error()
}

func sameError(e1, e2 error) bool {
	if codeOf(e1) != codeOf(e2) {
		return false
	}
	if codeOf(e1) == 0 {
		return true
	}
	return e1.(*Error).Index == e2.(*Error).Index
}

// TestRandomDifferential 用大量随机操作序列对照生产服务与独立朴素模型，
// 每步记录输入、输出（错误码+下标）与判定依据；任何状态分叉立即失败。
// 使用 -v 运行可看到完整逐步日志。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential fuzz in short mode")
	}
	const (
		seeds     = 40
		maxSteps  = 300
		nTeachers = 5
		nTasks    = 9
		nPeriods  = 4
		maxWeeks  = 6
	)
	ranks := []string{"wide", "prof", "lect"}
	semesters := []string{"2024-1", "2024-2"}

	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var log strings.Builder
		fmt.Fprintf(&log, "=== seed %d ===\n", seed)

		cfg := testConfig()
		svc, err := NewService(cfg, 0)
		if err != nil {
			t.Fatal(err)
		}
		nm := newNaiveModel(cfg, 0)

		taskSpecs := map[string]TaskSpec{}
		now := int64(0)

		for step := 0; step < maxSteps; step++ {
			now += int64(rng.Intn(3))

			if rng.Intn(10) <= 1 && len(taskSpecs) < nTasks {
				idx := len(taskSpecs)
				id := fmt.Sprintf("t%d", idx)
				weeks := 1 + rng.Intn(maxWeeks)
				hours := weeks * (1 + rng.Intn(8))
				periods := []int{1 + rng.Intn(nPeriods)}
				if p2 := 1 + rng.Intn(nPeriods); p2 != periods[0] {
					periods = append(periods, p2)
				}
				spec := TaskSpec{
					ID: id, Semester: semesters[rng.Intn(2)], Hours: hours,
					ClassSize: []int{10, 30, 31, 60, 61, 120}[rng.Intn(6)],
					NewCourse: rng.Intn(3) == 0,
					IsLab:     rng.Intn(4) == 0,
					WeekStart: 1, WeekEnd: weeks, Periods: periods,
				}
				taskSpecs[id] = spec
				fmt.Fprintf(&log, "step %d t=%d AddTask %+v\n", step, now, spec)
				e1 := svc.AddTask(spec, now)
				e2 := nm.addTask(spec, now)
				fmt.Fprintf(&log, "  -> svc[%s] naive[%s]\n", errCodeIndex(e1), errCodeIndex(e2))
				if codeOf(e1) != codeOf(e2) {
					t.Fatalf("seed %d step %d AddTask divergence:\n%s", seed, step, log.String())
				}
				if diff := cmpSnapshot(svcSnapshot(t, svc), nm.snapshot()); diff != "" {
					t.Fatalf("seed %d step %d state divergence: %s\n%s", seed, step, diff, log.String())
				}
				continue
			}
			if len(taskSpecs) == 0 {
				continue
			}

			tids := make([]string, 0, len(taskSpecs))
			for id := range taskSpecs {
				tids = append(tids, id)
			}
			sort.Strings(tids)
			tid := tids[rng.Intn(len(tids))]
			tSpec := taskSpecs[tid]
			teacher := fmt.Sprintf("h%d", rng.Intn(nTeachers))

			rank := ranks[int(teacher[1]-'0')%len(ranks)] // 两侧必须使用同一职级
			pre1 := svc.AddTeacher(teacher, rank, now)
			pre2 := nm.addTeacher(teacher, rank, now)
			if codeOf(pre1) != codeOf(pre2) ||
				(codeOf(pre1) != 0 && codeOf(pre1) != ErrIllegalState) {
				t.Fatalf("seed %d AddTeacher pre divergence %v/%v:\n%s", seed, pre1, pre2, log.String())
			}

			switch action := rng.Intn(10); {
			case action < 5:
				var reqs []AssignmentReq
				if tSpec.Hours < 2 || rng.Intn(2) == 0 {
					reqs = []AssignmentReq{{TeacherID: teacher, TaskID: tid, Hours: tSpec.Hours}}
				} else {
					h1 := 1 + rng.Intn(tSpec.Hours-1)
					other := fmt.Sprintf("h%d", rng.Intn(nTeachers))
					oRank := ranks[int(other[1]-'0')%len(ranks)]
					_ = svc.AddTeacher(other, oRank, now)
					_ = nm.addTeacher(other, oRank, now)
					reqs = []AssignmentReq{
						{TeacherID: teacher, TaskID: tid, Hours: h1},
						{TeacherID: other, TaskID: tid, Hours: tSpec.Hours - h1},
					}
				}
				if rng.Intn(5) == 0 {
					reqs[0].Hours++ // 偶发制造不守恒
				}
				fmt.Fprintf(&log, "step %d t=%d Assign %+v\n", step, now, reqs)
				e1 := svc.Assign(reqs, now)
				e2 := nm.assign(reqs, now)
				fmt.Fprintf(&log, "  -> svc[%s] naive[%s]\n", errCodeIndex(e1), errCodeIndex(e2))
				if !sameError(e1, e2) {
					t.Fatalf("seed %d step %d Assign divergence:\n%s", seed, step, log.String())
				}
			case action < 7:
				accept := rng.Intn(2) == 0
				fmt.Fprintf(&log, "step %d t=%d Respond %s/%s accept=%v\n", step, now, teacher, tid, accept)
				e1 := svc.Respond(teacher, tid, accept, now)
				e2 := nm.respond(teacher, tid, accept, now)
				fmt.Fprintf(&log, "  -> svc[%s] naive[%s]\n", errCodeIndex(e1), errCodeIndex(e2))
				if !sameError(e1, e2) {
					t.Fatalf("seed %d step %d Respond divergence:\n%s", seed, step, log.String())
				}
			case action < 9:
				to := fmt.Sprintf("h%d", rng.Intn(nTeachers))
				toRank := ranks[int(to[1]-'0')%len(ranks)]
				_ = svc.AddTeacher(to, toRank, now)
				_ = nm.addTeacher(to, toRank, now)
				fw := 1 + rng.Intn(tSpec.WeekEnd)
				fmt.Fprintf(&log, "step %d t=%d Replace %s->%s task=%s week=%d\n",
					step, now, teacher, to, tid, fw)
				e1 := svc.Replace(teacher, tid, to, fw, now)
				e2 := nm.replace(teacher, tid, to, fw, now)
				fmt.Fprintf(&log, "  -> svc[%s] naive[%s]\n", errCodeIndex(e1), errCodeIndex(e2))
				if !sameError(e1, e2) {
					t.Fatalf("seed %d step %d Replace divergence:\n%s", seed, step, log.String())
				}
			default:
				sem := tSpec.Semester
				fmt.Fprintf(&log, "step %d t=%d Settle %s/%s\n", step, now, teacher, sem)
				r1, e1 := svc.Settle(teacher, sem, now)
				r2, e2 := nm.settle(teacher, sem, now)
				fmt.Fprintf(&log, "  -> svc[%s]%+v naive[%s]%+v\n",
					errCodeIndex(e1), r1, errCodeIndex(e2), r2)
				if codeOf(e1) != codeOf(e2) {
					t.Fatalf("seed %d step %d Settle err divergence:\n%s", seed, step, log.String())
				}
				if e1 == nil && *r1 != *r2 {
					t.Fatalf("seed %d step %d Settle result divergence:\n%+v\nvs\n%+v\n%s",
						seed, step, *r1, *r2, log.String())
				}
			}

			if diff := cmpSnapshot(svcSnapshot(t, svc), nm.snapshot()); diff != "" {
				t.Fatalf("seed %d step %d state divergence: %s\n%s", seed, step, diff, log.String())
			}
		}

		if testing.Verbose() {
			t.Logf("\n%s", log.String())
		}
	}
}
