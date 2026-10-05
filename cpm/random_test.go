package cpm

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

type naiveTask struct {
	duration int64
	snet     int64
	fnlt     int64
	es       int64
	ef       int64
	ls       int64
	lf       int64
	tf       int64
}

type naiveNetwork struct {
	n        int
	deadline int64
	tasks    []naiveTask
	edges    map[[2]int]int64
	pf       int64
	critical map[int]bool
}

func newNaive(deadline int64) *naiveNetwork {
	return &naiveNetwork{deadline: deadline, edges: map[[2]int]int64{}, critical: map[int]bool{}}
}

func (q *naiveNetwork) recompute() {
	if q.n == 0 {
		q.pf = 0
		q.critical = map[int]bool{}
		return
	}
	indegree := make([]int, q.n)
	for key := range q.edges {
		indegree[key[1]]++
	}
	queue := []int{}
	for v := 0; v < q.n; v++ {
		if indegree[v] == 0 {
			queue = append(queue, v)
		}
	}
	order := append([]int(nil), queue...)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for key := range q.edges {
			if key[0] != u {
				continue
			}
			v := key[1]
			indegree[v]--
			if indegree[v] == 0 {
				queue = append(queue, v)
				order = append(order, v)
			}
		}
	}
	for _, v := range order {
		es := q.tasks[v].snet
		for key, lag := range q.edges {
			if key[1] == v {
				if candidate := q.tasks[key[0]].ef + lag; candidate > es {
					es = candidate
				}
			}
		}
		q.tasks[v].es = es
		q.tasks[v].ef = es + q.tasks[v].duration
	}
	outdegree := make([]int, q.n)
	for key := range q.edges {
		outdegree[key[0]]++
	}
	queue = queue[:0]
	for v := 0; v < q.n; v++ {
		if outdegree[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		lf := q.deadline
		if q.tasks[u].fnlt >= 0 && q.tasks[u].fnlt < lf {
			lf = q.tasks[u].fnlt
		}
		for key, lag := range q.edges {
			if key[0] != u {
				continue
			}
			v := key[1]
			if candidate := q.tasks[v].ls - lag; candidate < lf {
				lf = candidate
			}
		}
		q.tasks[u].lf = lf
		q.tasks[u].ls = lf - q.tasks[u].duration
		outdegreeWait := 0
		_ = outdegreeWait
		for key := range q.edges {
			if key[1] != u {
				continue
			}
			p := key[0]
			outdegree[p]--
			if outdegree[p] == 0 {
				queue = append(queue, p)
			}
		}
	}
	q.pf = 0
	minTF := int64(0)
	for v := 0; v < q.n; v++ {
		if q.tasks[v].ef > q.pf {
			q.pf = q.tasks[v].ef
		}
		q.tasks[v].tf = q.tasks[v].lf - q.tasks[v].ef
		if v == 0 || q.tasks[v].tf < minTF {
			minTF = q.tasks[v].tf
		}
	}
	q.critical = map[int]bool{}
	for v := 0; v < q.n; v++ {
		if q.tasks[v].tf == minTF {
			q.critical[v] = true
		}
	}
}

func (q *naiveNetwork) addTask(duration int64) {
	q.tasks = append(q.tasks, naiveTask{duration: duration, fnlt: -1})
	q.n++
	q.recompute()
}

func (q *naiveNetwork) snapshotCritical() map[int]bool {
	result := make(map[int]bool, len(q.critical))
	for key := range q.critical {
		result[key] = true
	}
	return result
}

func reportNaive(oldPF int64, oldCritical map[int]bool, oldTasks []naiveTask, q *naiveNetwork) UpdateReport {
	report := UpdateReport{ChangedES: []int{}, ChangedLF: []int{}, OldPF: oldPF, NewPF: q.pf, CritAdded: []int{}, CritRemoved: []int{}}
	for v := 0; v < q.n; v++ {
		if v >= len(oldTasks) {
			report.ChangedES = append(report.ChangedES, v)
			report.ChangedLF = append(report.ChangedLF, v)
		} else {
			if oldTasks[v].es != q.tasks[v].es {
				report.ChangedES = append(report.ChangedES, v)
			}
			if oldTasks[v].lf != q.tasks[v].lf {
				report.ChangedLF = append(report.ChangedLF, v)
			}
		}
		if !oldCritical[v] && q.critical[v] {
			report.CritAdded = append(report.CritAdded, v)
		}
		if oldCritical[v] && !q.critical[v] {
			report.CritRemoved = append(report.CritRemoved, v)
		}
	}
	return report
}

func TestRandomizedComparison(t *testing.T) {
	rng := rand.New(rand.NewPCG(1194, 20261005))
	const sequences = 2000
	var failedOps []string
	for iteration := 0; iteration < sequences; iteration++ {
		deadline := []int64{0, 8, 20, 100}[rng.IntN(4)]
		m, err := NewMaintainer(12, 30, deadline)
		if err != nil {
			t.Fatal(err)
		}
		q := newNaive(deadline)
		steps := 24
		for step := 0; step < steps; step++ {
			oldPF := q.pf
			oldCritical := q.snapshotCritical()
			oldTasks := append([]naiveTask(nil), q.tasks...)
			var report *UpdateReport
			var input string
			xFwd := map[int]struct{}{}
			xBwd := map[int]struct{}{}
			outdegree := make([]int, q.n)
			indegree := make([]int, q.n)
			for key := range q.edges {
				outdegree[key[0]]++
				indegree[key[1]]++
			}
			choice := rng.IntN(10)
			switch {
			case choice < 3 && q.n < 12:
				duration := int64(rng.IntN(7))
				input = fmt.Sprintf("AddTask(%d)", duration)
				_, report, err = m.AddTask(duration)
				xFwd[q.n] = struct{}{}
				xBwd[q.n] = struct{}{}
				outdegree = append(outdegree, 0)
				indegree = append(indegree, 0)
				if err != nil {
					t.Fatalf("%s unexpected: %v", input, err)
				}
				q.addTask(duration)
				failedOps = append(failedOps, input)
			case q.n > 0 && choice < 6:
				u, v := rng.IntN(q.n), rng.IntN(q.n)
				lag := int64(rng.IntN(7) - 3)
				xFwd[v] = struct{}{}
				xBwd[u] = struct{}{}
				xBwd[v] = struct{}{}
				input = fmt.Sprintf("AddDep(%d,%d,%d)", u, v, lag)
				_, merr := m.AddDep(u, v, lag)
				_, exists := q.edges[[2]int{u, v}]
				cycle := u == v
				if !exists && !cycle {
					if reachable(q, v, u) {
						cycle = true
					}
				}
				if exists {
					if merr != ErrDependencyExists {
						t.Fatalf("%s error=%v want exists", input, merr)
					}
				} else if cycle {
					if merr != ErrCycle {
						t.Fatalf("%s error=%v want cycle", input, merr)
					}
				} else if merr != nil {
					t.Fatalf("%s unexpected error %v", input, merr)
				} else {
					q.edges[[2]int{u, v}] = lag
					q.recompute()
					failedOps = append(failedOps, input)
				}
				report = nil
			case q.n > 0 && choice < 8:
				v := rng.IntN(q.n)
				xFwd[v] = struct{}{}
				xBwd[v] = struct{}{}
				duration := int64(rng.IntN(7))
				input = fmt.Sprintf("SetDuration(%d,%d)", v, duration)
				report, err = m.SetDuration(v, duration)
				if err != nil {
					t.Fatal(err)
				}
				q.tasks[v].duration = duration
				q.recompute()
				failedOps = append(failedOps, input)
			case q.n > 0 && choice < 9:
				v := rng.IntN(q.n)
				xFwd[v] = struct{}{}
				xBwd[v] = struct{}{}
				snet := int64(rng.IntN(10))
				fnlt := int64(rng.IntN(22) - 1)
				input = fmt.Sprintf("SetConstraint(%d,%d,%d)", v, snet, fnlt)
				report, err = m.SetConstraint(v, snet, fnlt)
				if err != nil {
					t.Fatal(err)
				}
				q.tasks[v].snet = snet
				q.tasks[v].fnlt = fnlt
				q.recompute()
				failedOps = append(failedOps, input)
			default:
				keys := make([][2]int, 0, len(q.edges))
				for key := range q.edges {
					keys = append(keys, key)
				}
				if len(keys) == 0 {
					continue
				}
				key := keys[rng.IntN(len(keys))]
				xFwd[key[1]] = struct{}{}
				xBwd[key[0]] = struct{}{}
				xBwd[key[1]] = struct{}{}
				input = fmt.Sprintf("RemoveDep(%d,%d)", key[0], key[1])
				report, err = m.RemoveDep(key[0], key[1])
				if err != nil {
					t.Fatal(err)
				}
				delete(q.edges, key)
				q.recompute()
				failedOps = append(failedOps, input)
			}

			if testing.Verbose() {
				t.Logf("iter=%d step=%d %s report=%+v reason=%s", iteration, step, input, report, "incremental state compared against full DAG recomputation")
			}
			mismatch := compareNaive(t, m, q)
			if mismatch {
				t.Fatalf("%s state mismatch; deadline=%d ops=%v", input, deadline, failedOps)
			}
			if report != nil {
				want := reportNaive(oldPF, oldCritical, oldTasks, q)
				if !reflect.DeepEqual(*report, want) {
					t.Fatalf("%s report=%+v want=%+v", input, *report, want)
				}
				assertEvaluationBoundAgainstCurrent(t, m, report, xFwd, xBwd, outdegree, indegree)
			}
		}
	}
}

func reachable(q *naiveNetwork, from, to int) bool {
	seen := map[int]bool{from: true}
	queue := []int{from}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for key := range q.edges {
			if key[0] == u && !seen[key[1]] {
				if key[1] == to {
					return true
				}
				seen[key[1]] = true
				queue = append(queue, key[1])
			}
		}
	}
	return false
}

func compareNaive(t *testing.T, m *Maintainer, q *naiveNetwork) bool {
	t.Helper()
	snapshot := m.Snapshot()
	if len(snapshot.ES) != q.n {
		t.Fatalf("n=%d want %d", len(snapshot.ES), q.n)
	}
	for v := 0; v < q.n; v++ {
		task := q.tasks[v]
		if snapshot.ES[v] != task.es || snapshot.EF[v] != task.ef ||
			snapshot.LS[v] != task.ls || snapshot.LF[v] != task.lf ||
			snapshot.TF[v] != task.tf {
			t.Logf("mismatch task %d snapshot=%+v naive=%+v edges=%+v", v, snapshot, task, snapshot.Edges)
			return true
		}
	}
	if snapshot.PF != q.pf {
		t.Fatalf("PF=%d want %d", snapshot.PF, q.pf)
	}
	if len(snapshot.Edges) != len(q.edges) {
		t.Fatalf("edge count=%d want %d", len(snapshot.Edges), len(q.edges))
	}
	path := m.CriticalPath()
	verifyCriticalPath(t, q, path)
	return false
}

func verifyCriticalPath(t *testing.T, q *naiveNetwork, path []int) {
	t.Helper()
	if q.n == 0 {
		if len(path) != 0 {
			t.Fatalf("path=%v", path)
		}
		return
	}
	seen := map[int]bool{}
	for i, v := range path {
		if !q.critical[v] || seen[v] {
			t.Fatalf("invalid path %v at %d", path, v)
		}
		seen[v] = true
		if i > 0 {
			u := path[i-1]
			lag, exists := q.edges[[2]int{u, v}]
			if !exists || q.tasks[v].es != q.tasks[u].ef+lag {
				t.Fatalf("edge %d->%d in path %v is not critical/driving", u, v, path)
			}
		}
	}
	smallestStart := -1
	for v := 0; v < q.n; v++ {
		if !q.critical[v] {
			continue
		}
		hasIncoming := false
		for key, lag := range q.edges {
			if key[1] == v && q.critical[key[0]] && q.tasks[v].es == q.tasks[key[0]].ef+lag {
				hasIncoming = true
			}
		}
		if !hasIncoming {
			smallestStart = v
			break
		}
	}
	if len(path) == 0 || path[0] != smallestStart {
		t.Fatalf("path=%v start should be %d", path, smallestStart)
	}
}

func assertEvaluationBoundAgainstCurrent(t *testing.T, m *Maintainer, report *UpdateReport, xFwd, xBwd map[int]struct{}, outdegree, indegree []int) {
	t.Helper()
	fwd, bwd := m.EvaluationCounts()
	for _, v := range report.ChangedES {
		xFwd[v] = struct{}{}
	}
	for _, v := range report.ChangedLF {
		xBwd[v] = struct{}{}
	}
	fwdBound, bwdBound := 0, 0
	for v := range xFwd {
		fwdBound += 1 + outdegree[v]
	}
	for v := range xBwd {
		bwdBound += 1 + indegree[v]
	}
	if fwd > fwdBound || bwd > bwdBound {
		t.Fatalf("evaluations=(%d,%d) bounds=(%d,%d) report=%+v", fwd, bwd, fwdBound, bwdBound, report)
	}
}
