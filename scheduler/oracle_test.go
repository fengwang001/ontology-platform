package scheduler

import (
	"math/rand"
	"testing"
)

type oracleState struct {
	n                  int
	duration           []int64
	snet               []int64
	fnlt               []int64
	preds              [][]int
	succs              [][]int
	lag                map[[2]int]int64
	es, ef, ls, lf, tf []int64
	pf                 int64
	critical           []bool
	criticalCount      int
}

func newOracle() *oracleState {
	return &oracleState{lag: map[[2]int]int64{}}
}

func (o *oracleState) recalc(deadline int64) {
	n := o.n
	o.es = make([]int64, n)
	o.ef = make([]int64, n)
	o.ls = make([]int64, n)
	o.lf = make([]int64, n)
	o.tf = make([]int64, n)
	indegree := make([]int, n)
	queue := []int{}
	for v := 0; v < n; v++ {
		indegree[v] = len(o.preds[v])
		if indegree[v] == 0 {
			queue = append(queue, v)
		}
	}
	order := []int{}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		order = append(order, v)
		for _, w := range o.succs[v] {
			indegree[w]--
			if indegree[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	o.pf = 0
	for _, v := range order {
		value := o.snet[v]
		for _, u := range o.preds[v] {
			candidate := o.ef[u] + o.lag[[2]int{u, v}]
			if candidate > value {
				value = candidate
			}
		}
		o.es[v] = value
		o.ef[v] = value + o.duration[v]
		if o.ef[v] > o.pf {
			o.pf = o.ef[v]
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		value := deadline
		if o.fnlt[v] >= 0 && o.fnlt[v] < value {
			value = o.fnlt[v]
		}
		for _, w := range o.succs[v] {
			candidate := o.ls[w] - o.lag[[2]int{v, w}]
			if candidate < value {
				value = candidate
			}
		}
		o.lf[v] = value
		o.ls[v] = value - o.duration[v]
		o.tf[v] = value - o.ef[v]
	}
	minTF := int64(0)
	for v := 0; v < n; v++ {
		if v == 0 || o.tf[v] < minTF {
			minTF = o.tf[v]
		}
	}
	o.critical = make([]bool, n)
	o.criticalCount = 0
	for v := 0; v < n; v++ {
		o.critical[v] = n > 0 && o.tf[v] == minTF
		if o.critical[v] {
			o.criticalCount++
		}
	}
}

func (o *oracleState) addTask(dur int64) {
	v := o.n
	o.n++
	o.duration = append(o.duration, dur)
	o.snet = append(o.snet, 0)
	o.fnlt = append(o.fnlt, -1)
	o.preds = append(o.preds, nil)
	o.succs = append(o.succs, nil)
	_ = v
}

func (o *oracleState) addDep(u, v int, lag int64) {
	o.lag[[2]int{u, v}] = lag
	o.preds[v] = append(o.preds[v], u)
	o.succs[u] = append(o.succs[u], v)
}

func (o *oracleState) removeDep(u, v int) {
	delete(o.lag, [2]int{u, v})
	o.preds[v] = deleteInt(o.preds[v], u)
	o.succs[u] = deleteInt(o.succs[u], v)
}

func deleteInt(values []int, value int) []int {
	for i, x := range values {
		if x == value {
			return append(values[:i], values[i+1:]...)
		}
	}
	return values
}

func boolNodes(values []bool) []int {
	out := []int{}
	for v, ok := range values {
		if ok {
			out = append(out, v)
		}
	}
	return out
}

func diffSets(before, after []bool) (added, removed []int) {
	added, removed = []int{}, []int{}
	n := len(after)
	for v := 0; v < n; v++ {
		old := v < len(before) && before[v]
		if !old && after[v] {
			added = append(added, v)
		}
		if old && !after[v] {
			removed = append(removed, v)
		}
	}
	return added, removed
}

func TestRandomSequencesAgainstNaiveRecalculation(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < iterations; iter++ {
		deadline := int64(rng.Intn(30))
		s, err := New(8, 24, deadline)
		if err != nil {
			t.Fatal(err)
		}
		o := newOracle()
		steps := 5 + rng.Intn(16)
		for step := 0; step < steps; step++ {
			oldES := append([]int64(nil), s.es...)
			oldLF := append([]int64(nil), s.lf...)
			oldCrit := append([]bool(nil), s.critical...)
			oldPF := s.PF()

			var report *UpdateReport
			directX := []int{}
			input := ""
			switch rng.Intn(6) {
			case 0:
				dur := int64(rng.Intn(6))
				var v int
				v, report, err = s.AddTask(dur)
				directX = []int{v}
				if err == nil {
					o.addTask(dur)
				}
				input = "AddTask"
			case 1:
				if o.n == 0 {
					continue
				}
				u := rng.Intn(o.n)
				v := rng.Intn(o.n)
				directX = []int{u, v}
				lag := int64(rng.Intn(7) - 3)
				_, exists := o.lag[[2]int{u, v}]
				report, err = s.AddDep(u, v, lag)
				if err == nil {
					if exists {
						t.Fatalf("oracle duplicate edge (%d,%d)", u, v)
					}
					o.addDep(u, v, lag)
				}
				input = "AddDep"
			case 2:
				if o.n == 0 {
					continue
				}
				v := rng.Intn(o.n)
				directX = []int{v}
				dur := int64(rng.Intn(6))
				report, err = s.SetDuration(v, dur)
				if err == nil {
					o.duration[v] = dur
				}
				input = "SetDuration"
			case 3:
				if o.n == 0 {
					continue
				}
				v := rng.Intn(o.n)
				directX = []int{v}
				snet := int64(rng.Intn(8))
				fnlt := int64(rng.Intn(int(deadline + 6)))
				if rng.Intn(4) == 0 {
					fnlt = -1
				}
				report, err = s.SetConstraint(v, snet, fnlt)
				if err == nil {
					o.snet[v] = snet
					o.fnlt[v] = fnlt
				}
				input = "SetConstraint"
			case 4:
				if o.n == 0 {
					continue
				}
				u := rng.Intn(o.n)
				v := rng.Intn(o.n)
				directX = []int{u, v}
				report, err = s.RemoveDep(u, v)
				if err == nil {
					o.removeDep(u, v)
				}
				input = "RemoveDep"
			default:
				s.SetBaseline()
				continue
			}
			if err != nil {
				continue
			}
			wasAddTask := input == "AddTask"
			o.recalc(deadline)
			wantES := diffExistingInt64(oldES, o.es)
			wantLF := diffExistingInt64(oldLF, o.lf)
			var added, removed []int
			if wasAddTask {
				newTask := o.n - 1
				added, removed = []int{}, []int{}
				if o.critical[newTask] {
					added = append(added, newTask)
				}
				for v := range oldCrit {
					if oldCrit[v] && !o.critical[v] {
						removed = append(removed, v)
					}
				}
			} else {
				added, removed = diffSets(oldCrit, o.critical)
			}
			t.Logf("iter=%d step=%d 输入=%s 输出=%+v", iter, step, input, *report)
			intsEqual(t, report.ChangedES, wantES, "oracle ChangedES")
			intsEqual(t, report.ChangedLF, wantLF, "oracle ChangedLF")
			int64Equal(t, report.OldPF, oldPF, "oracle OldPF")
			int64Equal(t, report.NewPF, o.pf, "oracle NewPF")
			intsEqual(t, report.CritAdded, added, "oracle CritAdded")
			intsEqual(t, report.CritRemoved, removed, "oracle CritRemoved")
			assertBudget(t, s, directX, report.ChangedES, report.ChangedLF, "random "+input)
			for v := 0; v < o.n; v++ {
				es, _ := s.ES(v)
				ef, _ := s.EF(v)
				ls, _ := s.LS(v)
				lf, _ := s.LF(v)
				tf, _ := s.TF(v)
				int64Equal(t, es, o.es[v], "oracle ES")
				int64Equal(t, ef, o.ef[v], "oracle EF")
				int64Equal(t, ls, o.ls[v], "oracle LS")
				int64Equal(t, lf, o.lf[v], "oracle LF")
				int64Equal(t, tf, o.tf[v], "oracle TF")
				ff, _ := s.FF(v)
				wantFF := o.pf - o.ef[v]
				for _, w := range o.succs[v] {
					value := o.es[w] - o.lag[[2]int{v, w}] - o.ef[v]
					if wantFF == o.pf-o.ef[v] || value < wantFF {
						wantFF = value
					}
				}
				int64Equal(t, ff, wantFF, "oracle FF")
			}
			intsEqual(t, s.CriticalTasks(), boolNodes(o.critical), "oracle critical")
			intsEqual(t, s.CriticalPath(), oraclePath(o), "oracle path")
		}
	}
}

func diffExistingInt64(before, after []int64) []int {
	out := []int{}
	for v := range before {
		if before[v] != after[v] {
			out = append(out, v)
		}
	}
	return out
}

func oraclePath(o *oracleState) []int {
	critical := boolNodes(o.critical)
	if len(critical) == 0 {
		return []int{}
	}
	set := make([]bool, o.n)
	for _, v := range critical {
		set[v] = true
	}
	start := -1
	for _, v := range critical {
		found := false
		for _, u := range o.preds[v] {
			if set[u] && o.es[v] == o.ef[u]+o.lag[[2]int{u, v}] {
				found = true
			}
		}
		if !found {
			start = v
			break
		}
	}
	path := []int{}
	current := start
	seen := make([]bool, o.n)
	for current >= 0 && !seen[current] {
		seen[current] = true
		path = append(path, current)
		next := -1
		for _, w := range o.succs[current] {
			if set[w] && o.es[w] == o.ef[current]+o.lag[[2]int{current, w}] && (next < 0 || w < next) {
				next = w
			}
		}
		current = next
	}
	return path
}
