package mcsched

import (
	"math/rand"
	"strconv"
	"testing"
)

// naiveJob 是朴素模拟器的作业表示：用 map 管理，每次全量扫描。
type naiveJob struct {
	id   string
	hi   bool
	k    int
	d    int
	exec int
	rem  int
}

type naiveSim struct {
	tasks  []Task
	demand map[string]map[int]int
	mode   Mode
	active map[*naiveJob]struct{}
	stats  Stats
	trace  []string
}

func newNaive(tasks []Task, demand map[string]map[int]int) *naiveSim {
	return &naiveSim{
		tasks:  tasks,
		demand: demand,
		mode:   ModeLO,
		active: map[*naiveJob]struct{}{},
	}
}

func (s *naiveSim) dem(id string, k, cl int) int {
	if ks, ok := s.demand[id]; ok {
		if c, ok := ks[k]; ok {
			return c
		}
	}
	return cl
}

// run 严格按题目原文逐 tick 书写，刻意不与实现共享任何代码。
func (s *naiveSim) run(n int) {
	for tick := 0; tick < n; tick++ {
		t := len(s.trace)

		// 一：对每个有未完成作业且 d=t 的任务，该作业记为错过并移除。
		for j := range s.active {
			if j.d == t {
				if j.hi {
					s.stats.MissedHI++
				} else {
					s.stats.MissedLO++
				}
				delete(s.active, j)
			}
		}

		// 二：HI 模式且此刻没有任何未完成作业 -> 恢复 LO。
		if s.mode == ModeHI && len(s.active) == 0 {
			s.mode = ModeLO
			s.stats.Recoveries++
		}

		// 三：释放作业（HI 模式下 LO 任务丢弃，序号照占）。
		for _, tp := range s.tasks {
			if t < tp.Phi || (t-tp.Phi)%tp.T != 0 {
				continue
			}
			k := (t - tp.Phi) / tp.T
			if s.mode == ModeHI && tp.Lvl == LO {
				s.stats.Skipped++
				continue
			}
			s.active[&naiveJob{
				id:  tp.ID,
				hi:  tp.Lvl == HI,
				k:   k,
				d:   t + tp.T,
				rem: s.dem(tp.ID, k, tp.CL),
			}] = struct{}{}
		}

		// 取 prio 最小者运行一个 tick。
		var cur *naiveJob
		prio := 0
		for j := range s.active {
			var p int
			for _, tp := range s.tasks {
				if tp.ID == j.id {
					p = tp.Prio
				}
			}
			if cur == nil || p < prio {
				cur, prio = j, p
			}
		}
		if cur == nil {
			s.trace = append(s.trace, "")
			continue
		}
		cur.exec++
		cur.rem--
		s.trace = append(s.trace, cur.id)
		if cur.rem == 0 {
			delete(s.active, cur)
			s.stats.Completions++
			continue
		}
		var cl int
		for _, tp := range s.tasks {
			if tp.ID == cur.id {
				cl = tp.CL
			}
		}
		if s.mode == ModeLO && cur.hi && cur.exec == cl {
			s.mode = ModeHI
			s.stats.Switches++
			for j := range s.active {
				if !j.hi {
					delete(s.active, j)
					s.stats.Discarded++
				}
			}
		}
	}
}

// genCase 随机生成合法任务集与需求覆盖。
func genCase(rng *rand.Rand) ([]Task, map[string]map[int]int, int) {
	nTasks := 1 + rng.Intn(6)
	usedID := map[string]bool{}
	usedPrio := map[int]bool{}
	tasks := make([]Task, 0, nTasks)
	for len(tasks) < nTasks {
		lvl := Level(rng.Intn(2))
		tp := Task{
			ID:   "t" + strconv.Itoa(len(tasks)) + "_" + strconv.Itoa(rng.Intn(100000)),
			Prio: 1 + rng.Intn(50),
			Phi:  rng.Intn(6),
		}
		if usedPrio[tp.Prio] {
			continue
		}
		tp.T = 1 + rng.Intn(12)
		if lvl == LO {
			tp.Lvl = LO
			tp.CL = 1 + rng.Intn(tp.T)
			tp.CH = tp.CL
		} else {
			tp.Lvl = HI
			tp.CL = 1 + rng.Intn(tp.T)
			tp.CH = tp.CL + rng.Intn(tp.T-tp.CL+1)
		}
		if usedID[tp.ID] {
			continue
		}
		usedID[tp.ID] = true
		usedPrio[tp.Prio] = true
		tasks = append(tasks, tp)
	}

	// 为每个任务的若干个早期作业随机设置需求（含 HI 超出 CL 的情形）。
	demand := map[string]map[int]int{}
	horizon := 1 + rng.Intn(60)
	for _, tp := range tasks {
		maxK := 0
		for tp.Phi+maxK*tp.T < horizon {
			maxK++
			if maxK > 8 {
				break
			}
		}
		for k := 0; k < maxK; k++ {
			if rng.Intn(2) == 0 {
				continue
			}
			c := 1 + rng.Intn(tp.CH)
			if demand[tp.ID] == nil {
				demand[tp.ID] = map[int]int{}
			}
			demand[tp.ID][k] = c
		}
	}
	// 约一半用例：挑一个 CH>CL 的 HI 任务，强制其 k=0 作业超 CL，
	// 以提高模式切换/舍弃/跳过场景的覆盖率。
	if rng.Intn(2) == 0 {
		var cand []Task
		for _, tp := range tasks {
			if tp.Lvl == HI && tp.CH > tp.CL && tp.Phi < horizon {
				cand = append(cand, tp)
			}
		}
		if len(cand) > 0 {
			tp := cand[rng.Intn(len(cand))]
			if demand[tp.ID] == nil {
				demand[tp.ID] = map[int]int{}
			}
			demand[tp.ID][0] = tp.CL + 1 + rng.Intn(tp.CH-tp.CL)
		}
	}
	return tasks, demand, horizon
}

func TestNaiveDifferential2000(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261003))
	for c := 0; c < cases; c++ {
		tasks, demand, horizon := genCase(rng)

		impl := New()
		for _, tp := range tasks {
			if err := impl.AddTask(tp); err != nil {
				t.Fatalf("case %d AddTask(%+v): %v", c, tp, err)
			}
		}
		for id, ks := range demand {
			for k, v := range ks {
				if err := impl.SetDemand(id, k, v); err != nil {
					t.Fatalf("case %d SetDemand(%s,%d,%d): %v", c, id, k, v, err)
				}
			}
		}
		// 用随机切分的 Step 调用验证可加性。
		rem := horizon
		for rem > 0 {
			n := 1 + rng.Intn(rem)
			if err := impl.Step(n); err != nil {
				t.Fatalf("case %d Step(%d): %v", c, n, err)
			}
			rem -= n
		}

		ref := newNaive(tasks, demand)
		ref.run(horizon)

		implTrace := traceImplCoded(impl, tasks, horizon)
		refTrace := traceNaive(ref, tasks, horizon)
		if implTrace != refTrace || impl.Stats() != ref.stats || impl.Mode() != ref.mode {
			t.Fatalf(
				"case %d 判定不一致\n输入 tasks=%+v\ndemand=%+v horizon=%d\n实现 trace=%q\n参考 trace=%q\n实现 stats=%+v mode=%v\n参考 stats=%+v mode=%v",
				c, tasks, demand, horizon,
				implTrace, refTrace, impl.Stats(), impl.Mode(),
				ref.stats, ref.mode)
		}
		if c < 3 || c == cases-1 {
			t.Logf("case %d 输入=%+v demand=%+v h=%d => trace=%q stats=%+v mode=%v 判定: 一致",
				c, tasks, demand, horizon, implTrace, impl.Stats(), impl.Mode())
		}
	}
}

// 用任务在 tasks 中的索引做单字母编码（最多 6 个任务，a-f），空闲记 '.'。
func codeOf(tasks []Task, id string) byte {
	for i, tp := range tasks {
		if tp.ID == id {
			return byte('a' + i)
		}
	}
	return '?'
}

func traceImplCoded(m *Monitor, tasks []Task, n int) string {
	b := make([]byte, n)
	for t := 0; t < n; t++ {
		id, ok := m.RunAt(t)
		if !ok {
			b[t] = '.'
		} else {
			b[t] = codeOf(tasks, id)
		}
	}
	return string(b)
}

func traceNaive(s *naiveSim, tasks []Task, n int) string {
	b := make([]byte, n)
	for t := 0; t < n; t++ {
		id := s.trace[t]
		if id == "" {
			b[t] = '.'
		} else {
			b[t] = codeOf(tasks, id)
		}
	}
	return string(b)
}
