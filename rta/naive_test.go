package rta

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveFixedPoint mirrors the reference procedure from the specification:
// sweep w upward one unit at a time from C+B and return the first w with
// demand(w) <= w; the deadline test applies at every inspected w including
// the initial one. Returns (w, schedulable).
func naiveFixedPoint(tk Task, hp []Task) (int64, bool) {
	base := tk.C + tk.B
	for w := base; ; w++ {
		if w+tk.J > tk.D {
			return w, false
		}
		demand := base
		for _, h := range hp {
			demand += ((w + h.J + h.T - 1) / h.T) * h.C
		}
		if demand <= w {
			return w, true
		}
	}
}

func naiveResponses(tasks []Task, order []int) (map[int]int64, bool) {
	resp := map[int]int64{}
	for pos, idx := range order {
		hp := make([]Task, pos)
		for k, h := range order[:pos] {
			hp[k] = tasks[h]
		}
		w, ok := naiveFixedPoint(tasks[idx], hp)
		if !ok {
			return nil, false
		}
		resp[idx] = w + tasks[idx].J
	}
	return resp, true
}

// bruteForceFeasible reports whether any permutation of all tasks is
// schedulable, for n <= 6.
func bruteForceFeasible(tasks []Task) bool {
	idx := make([]int, len(tasks))
	for i := range idx {
		idx[i] = i
	}
	var perms [][]int
	var gen func(int)
	gen = func(k int) {
		if k == len(idx) {
			perms = append(perms, append([]int(nil), idx...))
			return
		}
		for i := k; i < len(idx); i++ {
			idx[k], idx[i] = idx[i], idx[k]
			gen(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	gen(0)
	for _, perm := range perms {
		if _, ok := naiveResponses(tasks, perm); ok {
			return true
		}
	}
	return false
}

// naiveAudsley reproduces the Audsley fallback using the naive fixed point,
// returning high-to-low order and responses.
func naiveAudsley(tasks []Task) ([]int, map[int]int64, bool) {
	remaining := map[int]bool{}
	for i := range tasks {
		remaining[i] = true
	}
	built := []int{}
	for len(remaining) > 0 {
		chosen := -1
		var chosenR int64
		for idx := range remaining {
			hp := []Task{}
			for other := range remaining {
				if other != idx {
					hp = append(hp, tasks[other])
				}
			}
			w, ok := naiveFixedPoint(tasks[idx], hp)
			if !ok {
				continue
			}
			if chosen == -1 || tasks[idx].ID < tasks[chosen].ID {
				chosen = idx
				chosenR = w + tasks[idx].J
			}
		}
		if chosen == -1 {
			return nil, nil, false
		}
		delete(remaining, chosen)
		built = append(built, chosen)
		_ = chosenR
	}
	for i, j := 0, len(built)-1; i < j; i, j = i+1, j-1 {
		built[i], built[j] = built[j], built[i]
	}
	resp, ok := naiveResponses(tasks, built)
	return built, resp, ok
}

// model is the step-by-step reference simulation of Add/Remove/Order.
type model struct {
	tasks []Task
	order []int
	resp  map[int]int64
	log   *strings.Builder
}

func (m *model) add(t Task) (reordered bool, ok bool) {
	newIdx := len(m.tasks)
	cand := append(append([]Task(nil), m.tasks...), t)
	// Insertion from the lowest level upward.
	for attempt := 0; attempt <= len(m.order); attempt++ {
		p := len(m.order) - attempt
		trial := append(append(append([]int{}, m.order[:p]...), newIdx), m.order[p:]...)
		if r, good := naiveResponses(cand, trial); good {
			m.tasks = cand
			m.order = trial
			m.resp = r
			fmt.Fprintf(m.log, "  MODEL Add(%s) insert@%d -> %v\n", t.ID, p, m.ids())
			return false, true
		}
	}
	order, resp, good := naiveAudsley(cand)
	if !good {
		fmt.Fprintf(m.log, "  MODEL Add(%s) rejected unschedulable\n", t.ID)
		return false, false
	}
	m.tasks = cand
	m.order = order
	m.resp = resp
	fmt.Fprintf(m.log, "  MODEL Add(%s) AUDLSLEY -> %v\n", t.ID, m.ids())
	return true, true
}

func (m *model) remove(idx int) {
	id := m.tasks[idx].ID
	pos := 0
	for m.order[pos] != idx {
		pos++
	}
	m.order = append(append([]int{}, m.order[:pos]...), m.order[pos+1:]...)
	r, ok := naiveResponses(m.tasks, m.order)
	if !ok {
		panic("model: removal unschedulable")
	}
	m.resp = r
	fmt.Fprintf(m.log, "  MODEL Remove(%s) -> %v\n", id, m.ids())
}

func (m *model) ids() []string {
	out := make([]string, len(m.order))
	for i, idx := range m.order {
		out[i] = m.tasks[idx].ID
	}
	return out
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential test")
	}
	rng := rand.New(rand.NewSource(20261003))
	var log strings.Builder

	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		fmt.Fprintf(&log, "SEQ %d\n", seq)
		a := NewAnalyzer()
		m := &model{log: &log}
		steps := 1 + rng.Intn(12)
		nextID := 0
		idPool := func() string {
			nextID++
			return fmt.Sprintf("t%d", nextID)
		}
		for step := 0; step < steps; step++ {
			roll := rng.Intn(100)
			switch {
			case roll < 65:
				tk := Task{
					ID: idPool(),
					C:  int64(1 + rng.Intn(5)),
					T:  int64(1 + rng.Intn(12)),
					J:  int64(rng.Intn(4)),
					B:  int64(rng.Intn(3)),
				}
				tk.D = int64(1 + rng.Intn(int(tk.T)))
				fmt.Fprintf(&log, " step %d Add %+v\n", step, tk)
				re, err := a.Add(tk)
				reM, okM := m.add(tk)
				if okM != (err == nil) {
					t.Fatalf("seq %d step %d: Add feasibility mismatch impl=%v model=%v\n%s", seq, step, err, okM, log.String())
				}
				if err == nil && re != reM {
					t.Fatalf("seq %d step %d: reorder mismatch impl=%v model=%v\n%s", seq, step, re, reM, log.String())
				}
				if err == nil {
					mustMatchState(t, a, m, seq, step, &log)
				} else if re {
					t.Fatalf("seq %d: rejected add reported reorder", seq)
				} else {
					// For n<=6 cross-check rejection against exhaustive search.
					if len(m.tasks)+1 <= 6 {
						cand := append(append([]Task(nil), m.tasks...), tk)
						if bruteForceFeasible(cand) {
							t.Fatalf("seq %d step %d: impl rejected but a feasible permutation exists\n%s", seq, step, log.String())
						}
					}
					if e := errReason(t, err, ReasonUnschedulable); e.Pending < 1 || e.Pending > len(m.tasks)+1 {
						t.Fatalf("seq %d: bad pending %d", seq, e.Pending)
					}
				}
			case roll < 85:
				if len(m.order) == 0 {
					continue
				}
				pos := rng.Intn(len(m.order))
				idx := m.order[pos]
				id := m.tasks[idx].ID
				fmt.Fprintf(&log, " step %d Remove %s\n", step, id)
				if err := a.Remove(id); err != nil {
					t.Fatalf("seq %d: Remove(%s): %v", seq, id, err)
				}
				m.remove(idx)
				mustMatchState(t, a, m, seq, step, &log)
			default:
				fmt.Fprintf(&log, " step %d queries\n", step)
				got := a.Order()
				want := m.ids()
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("seq %d: order mismatch %v vs %v\n%s", seq, got, want, log.String())
				}
				for i, idx := range m.order {
					r, err := a.Response(m.tasks[idx].ID)
					if err != nil || r != m.resp[idx] {
						t.Fatalf("seq %d: Response(%s)=%d,%v model=%d\n%s", seq, got[i], r, err, m.resp[idx], log.String())
					}
				}
			}
		}

		// Final exhaustive check when small: current order must be feasible
		// (already guaranteed by matching model), and every prefix too.
		if len(m.tasks) <= 6 {
			for end := 1; end <= len(m.order); end++ {
				prefix := m.order[:end]
				seen := map[int]bool{}
				for _, idx := range prefix {
					if seen[idx] {
						t.Fatalf("dup in prefix")
					}
					seen[idx] = true
				}
				if _, ok := naiveResponses(m.tasks, prefix); !ok {
					t.Fatalf("seq %d: prefix %d not independently schedulable\n%s", seq, end, log.String())
				}
			}
		}
	}
	t.Logf("differential log tail:\n%s", tailLog(&log, 40))
}

func mustMatchState(t *testing.T, a *Analyzer, m *model, seq, step int, log *strings.Builder) {
	t.Helper()
	got := a.Order()
	want := m.ids()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("seq %d step %d: order mismatch impl=%v model=%v\n%s", seq, step, got, want, log.String())
	}
	for i, idx := range m.order {
		r, err := a.Response(m.tasks[idx].ID)
		if err != nil || r != m.resp[idx] {
			t.Fatalf("seq %d step %d: Response(%s)=%d,%v model=%d\n%s", seq, step, got[i], r, err, m.resp[idx], log.String())
		}
	}
}

func tailLog(b *strings.Builder, n int) string {
	lines := strings.Split(b.String(), "\n")
	if len(lines) <= n {
		return b.String()
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// Determinism: identical operation sequences replay to identical state.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	run := func() []string {
		a := NewAnalyzer()
		var out []string
		for i := 0; i < 30; i++ {
			tk := Task{ID: fmt.Sprintf("k%d", i), C: int64(1 + rng.Intn(4)), T: int64(2 + rng.Intn(10)), J: int64(rng.Intn(3)), B: int64(rng.Intn(2))}
			tk.D = int64(1 + rng.Intn(int(tk.T)))
			re, err := a.Add(tk)
			if err == nil {
				out = append(out, fmt.Sprintf("add %s re=%v", tk.ID, re))
			}
			if i%5 == 4 && len(a.Order()) > 0 {
				id := a.Order()[0]
				_ = a.Remove(id)
				out = append(out, "rm "+id)
			}
		}
		for _, id := range a.Order() {
			r, _ := a.Response(id)
			out = append(out, fmt.Sprintf("R(%s)=%d", id, r))
		}
		sort.Strings(out)
		return out
	}
	first := run()
	rng = rand.New(rand.NewSource(7))
	second := run()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatal("replay produced different results")
	}
}
