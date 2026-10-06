package allocation

import (
	"fmt"
	"math/rand"
	"testing"
)

// diffLogger 打印每一步的输入、输出与判定依据。
type diffLogger struct {
	t    *testing.T
	echo bool
}

func (d diffLogger) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	d.t.Log("  [svc] " + msg)
}

// op 为一条可同时施加给正式服务与朴素模型的操作。
type op struct {
	kind      string
	now       int64
	task      TaskSpec
	assign    []AssignItem
	share     int64
	teacherID string
	rank      string
	from      int
	sem       string
}

// TestRandomDifferential 大量随机操作序列对照：
// 每一步正式服务与独立朴素模型必须给出同一错误类别；
// 每个学期结束时份额守恒签名必须一致。
func TestRandomDifferential(t *testing.T) {
	const seeds = 40
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := testConfig()
			cfg.ConfirmDeadline = int64(3 + rng.Intn(6))
			svc := NewService(cfg)
			var lines []string
			svc.SetTracer(recorder{&lines, t})
			nm := newNaiveModel(cfg)

			teacherRanks := []string{"P", "A"}
			numTeachers := 2 + rng.Intn(4)
			tid := func(i int) string { return fmt.Sprintf("t%d", i) }
			for i := 0; i < numTeachers; i++ {
				rank := teacherRanks[rng.Intn(2)]
				now := int64(1 + i)
				e1 := svc.AddTeacher(now, TeacherSpec{ID: tid(i), Rank: rank})
				e2 := nm.addTeacher(now, TeacherSpec{ID: tid(i), Rank: rank})
				mustSame(t, e1, e2, "addTeacher")
			}

			var steps int
			var clock int64 = int64(numTeachers)
			tasksCreated := []string{}
			assignableTasks := []string{}
			shareIDs := []int64{} // 正式服务的 share id（朴素模型按 1..n 同序生成，故一致）
			for steps = 0; steps < 220; steps++ {
				clock += int64(rng.Intn(4))
				if rng.Intn(20) == 0 {
					clock-- // 偶尔时钟回退（仍单调概率小）
				}
				kind := rng.Intn(10)
				switch {
				case kind < 3 || len(tasksCreated) == 0:
					// 新建任务
					id := fmt.Sprintf("c%d", len(tasksCreated))
					sw := 1 + rng.Intn(4)
					ew := sw + rng.Intn(6)
					if ew > maxWeek-2 {
						ew = maxWeek - 2
					}
					weeks := ew - sw + 1
					hours := weeks + rng.Intn(40)
					sizeTier := []int{0, 30, 60, 90}[rng.Intn(4)]
					size := sizeTier + rng.Intn(3) // 大量恰等边界
					periods := []int{1 + rng.Intn(6)}
					if rng.Intn(3) == 0 {
						periods = append(periods, 1+rng.Intn(6))
					}
					spec := TaskSpec{
						ID: id, Semester: "S1", Hours: hours, ClassSize: size,
						IsNew: rng.Intn(3) == 0, IsLab: rng.Intn(4) == 0,
						StartWeek: sw, EndWeek: ew, Periods: periods,
					}
					e1 := svc.AddTask(clock, spec)
					e2 := nm.addTask(clock, spec)
					if mustSame(t, e1, e2, fmt.Sprintf("addTask %+v", spec)) {
						tasksCreated = append(tasksCreated, id)
						assignableTasks = append(assignableTasks, id)
					}
				case kind < 6 && len(assignableTasks) > 0:
					// 批量指派：随机把一个任务拆给 1..3 名教师
					ti := rng.Intn(len(assignableTasks))
					taskID := assignableTasks[ti]
					tkSpec := nm.tasks[taskID]
					n := 1 + rng.Intn(3)
					if n > numTeachers {
						n = numTeachers
					}
					perm := rng.Perm(numTeachers)[:n]
					items, exact := randomSplit(rng, tkSpec.Hours, n, taskID, perm, tid)
					ids, e1 := svc.AssignBatch(clock, items)
					e2 := nm.assignBatch(clock, items)
					if mustSame(t, e1, e2, fmt.Sprintf("assign %+v exact=%v", items, exact)) && e1 == nil {
						assignableTasks = append(assignableTasks[:ti], assignableTasks[ti+1:]...)
						shareIDs = append(shareIDs, ids...)
					}
				case kind < 8 && len(shareIDs) > 0:
					si := rng.Intn(len(shareIDs))
					id := shareIDs[si]
					if rng.Intn(2) == 0 {
						e1 := svc.Confirm(clock, id)
						e2 := nm.respond(clock, id, true)
						mustSame(t, e1, e2, fmt.Sprintf("confirm %d", id))
					} else {
						e1 := svc.Reject(clock, id)
						e2 := nm.respond(clock, id, false)
						mustSame(t, e1, e2, fmt.Sprintf("reject %d", id))
					}
				case len(shareIDs) > 0:
					// 换人
					si := rng.Intn(len(shareIDs))
					id := shareIDs[si]
					newTi := rng.Intn(numTeachers)
					// 找任务周次
					var sw, ew int
					for _, sh := range nm.shares {
						if sh.id == id {
							sw = sh.startWeek
							ew = sh.endWeek
							break
						}
					}
					if sw == 0 {
						continue
					}
					if ew <= sw {
						continue // 单周份额无法保留非零前半段，换人无意义
					}
					from := sw + 1 + rng.Intn(ew-sw)
					newID, e1 := svc.ChangeTeacher(clock, id, tid(newTi), from)
					e2 := nm.changeTeacher(clock, id, tid(newTi), from)
					if mustSame(t, e1, e2, fmt.Sprintf("change %d->%s@%d", id, tid(newTi), from)) && e1 == nil {
						shareIDs = append(shareIDs, newID)
					}
				}

				// 周期性核验：份额学时守恒（每个有份额的任务，各有效段学时和=任务学时）。
				if !assertConservationOK(t, svc) {
					for _, l := range lines {
						t.Log(l)
					}
					sn := svc.Snapshot()
					for _, sh := range sn.Shares {
						t.Logf("share %+v", sh)
					}
					t.Fatalf("seed=%d step=%d conservation broken", seed, steps)
				}
				// 份额签名对照（ID 同序分配，直接比较）。
				if !sameShareState(svc, nm) {
					for _, l := range lines {
						t.Log(l)
					}
					t.Fatalf("seed=%d step=%d share state diverged", seed, steps)
				}
			}

			// 学期核算对照
			rep, e1 := svc.Settle(clock+100, "S1")
			nrep, _, e2 := nm.settle(clock+100, "S1")
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("settle err differ %v %v", e1, e2)
			}
			if e1 == nil {
				for _, tr := range rep.Teachers {
					nr := nrep[tr.TeacherID]
					if tr.Total != nr.total || tr.Deficit != nr.deficit || tr.Excess != nr.excess ||
						tr.CreditBrought != nr.brought || tr.CreditUsed != nr.used ||
						tr.Unmet != nr.unmet || tr.CreditNext != nr.next {
						for _, l := range lines {
							t.Log(l)
						}
						sn := svc.Snapshot()
						for _, sh := range sn.Shares {
							t.Logf("svc  %+v", sh)
						}
						for _, sh := range nm.shares {
							t.Logf("naive %+v wl=%d", sh, nm.workloadOf(sh))
						}
						t.Fatalf("settle mismatch teacher=%s got=%+v naive=%+v", tr.TeacherID, tr, nr)
					}
				}
			}
			// 核算幂等
			rep2, err := svc.Settle(clock+200, "S1")
			if err != nil || rep2.SettledAt != rep.SettledAt {
				t.Fatalf("settle not idempotent")
			}
		})
	}
}

type recorder struct {
	lines *[]string
	t     *testing.T
}

func (r recorder) Logf(format string, args ...any) {
	*r.lines = append(*r.lines, fmt.Sprintf(format, args...))
}

func mustSame(t *testing.T, a, b *Error, ctx string) bool {
	t.Helper()
	ca, cb := codeOf(a), codeOf(b)
	if ca != cb {
		t.Fatalf("%s: service code=%d naive code=%d", ctx, ca, cb)
	}
	if ca == ErrHoursConservation || ca == ErrConflict || ca == ErrOverCap {
		// 下标也应一致（批量）
		if a != nil && b != nil && a.Index != b.Index {
			t.Fatalf("%s: index %d vs %d", ctx, a.Index, b.Index)
		}
	}
	return ca == -1 || ca == ErrInvalidArgument && false
}

func randomSplit(rng *rand.Rand, total, n int, taskID string, perm []int, tid func(int) string) ([]AssignItem, bool) {
	items := make([]AssignItem, n)
	// 70% 概率做守恒切分，30% 随机制造不守恒
	if rng.Intn(10) < 7 {
		parts := make([]int, n)
		rem := total
		for i := 0; i < n-1; i++ {
			if rem-(n-1-i) <= 0 {
				parts[i] = 0
			} else {
				parts[i] = 1 + rng.Intn(rem-(n-1-i))
			}
			rem -= parts[i]
		}
		parts[n-1] = rem
		rng.Shuffle(n, func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
		for i, p := range perm {
			items[i] = AssignItem{TaskID: taskID, TeacherID: tid(p), Hours: parts[i]}
		}
		// 去掉 0 学时项（非法参数会掩盖守恒错误）
		out := items[:0]
		for _, it := range items {
			if it.Hours > 0 {
				out = append(out, it)
			}
		}
		return out, true
	}
	for i, p := range perm {
		items[i] = AssignItem{TaskID: taskID, TeacherID: tid(p), Hours: 1 + rng.Intn(total)}
	}
	return items, false
}

func assertConservationOK(t *testing.T, s *Service) bool {
	snap := s.Snapshot()
	sums := map[string]int{}
	incomplete := map[string]bool{}
	for _, sh := range snap.Shares {
		if sh.State == stateRejected || sh.State == stateReleased {
			incomplete[sh.TaskID] = true
			continue
		}
		sums[sh.TaskID] += sh.Hours
	}
	for taskID, h := range sums {
		if incomplete[taskID] {
			if h > snap.Tasks[taskID].Hours {
				t.Logf("partial-rejected task=%s sum=%d > total=%d",
					taskID, h, snap.Tasks[taskID].Hours)
				return false
			}
			continue
		}
		if h != snap.Tasks[taskID].Hours {
			t.Logf("conservation violated task=%s sum=%d total=%d",
				taskID, h, snap.Tasks[taskID].Hours)
			return false
		}
	}
	return true
}

func sameShareState(s *Service, m *naiveModel) bool {
	snap := s.Snapshot()
	if len(snap.Shares) != len(m.shares) {
		return false
	}
	for i, sh := range snap.Shares {
		ns := m.shares[i]
		if sh.ID != ns.id || sh.TaskID != ns.taskID || sh.TeacherID != ns.teacherID ||
			sh.Hours != ns.hours || sh.State != ns.state ||
			sh.StartWeek != ns.startWeek || sh.EndWeek != ns.endWeek {
			return false
		}
	}
	return true
}
