package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveState struct {
	n, e, created, edges, touched int
	alive                         []bool
	ord                           []int
	out, in                       []map[int]bool
}

type naiveAddResult struct {
	moved             []int
	forward, backward int
	err               error
	witness           []int
}

func newNaive(n, e int) *naiveState {
	return &naiveState{
		n:     n,
		e:     e,
		alive: make([]bool, n),
		ord:   make([]int, n),
		out:   make([]map[int]bool, n),
		in:    make([]map[int]bool, n),
	}
}

func (s *naiveState) live(x int) bool { return x >= 0 && x < s.created && s.alive[x] }

func (s *naiveState) addNode() (int, error) {
	if s.created == s.n {
		return 0, ErrNodeLimit
	}
	x := s.created
	s.created++
	s.alive[x] = true
	s.ord[x] = x
	s.out[x] = map[int]bool{}
	s.in[x] = map[int]bool{}
	return x, nil
}

func boolKeys(values map[int]bool) []int {
	keys := make([]int, 0, len(values))
	for x := range values {
		keys = append(keys, x)
	}
	sort.Ints(keys)
	return keys
}

func (s *naiveState) sortByOrd(values []int) {
	sort.Slice(values, func(i, j int) bool {
		if s.ord[values[i]] != s.ord[values[j]] {
			return s.ord[values[i]] < s.ord[values[j]]
		}
		return values[i] < values[j]
	})
}

func (s *naiveState) forward(start, ub int) (map[int]bool, []int) {
	seen := map[int]bool{start: true}
	parent := make([]int, s.created)
	for i := range parent {
		parent[i] = -1
	}
	parent[start] = start
	queue := []int{start}
	for head := 0; head < len(queue); head++ {
		layerEnd := len(queue)
		next := []int{}
		for ; head < layerEnd; head++ {
			for y := range s.out[queue[head]] {
				if s.ord[y] > ub || seen[y] {
					continue
				}
				seen[y] = true
				parent[y] = queue[head]
				next = append(next, y)
			}
		}
		sort.Ints(next)
		queue = append(queue, next...)
		head = layerEnd - 1
	}
	return seen, parent
}

func (s *naiveState) backward(start, lb int) map[int]bool {
	seen := map[int]bool{start: true}
	queue := []int{start}
	for head := 0; head < len(queue); head++ {
		for _, y := range boolKeys(s.in[queue[head]]) {
			if s.ord[y] < lb || seen[y] {
				continue
			}
			seen[y] = true
			queue = append(queue, y)
		}
	}
	return seen
}

func naivePath(parent []int, start, target int) []int {
	path := []int{}
	for x := target; x != start; x = parent[x] {
		path = append(path, x)
	}
	path = append(path, start)
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (s *naiveState) addEdge(u, v int) naiveAddResult {
	if !s.live(u) || !s.live(v) {
		return naiveAddResult{err: ErrNodeNotFound}
	}
	if u == v {
		return naiveAddResult{err: &CycleError{Witness: []int{u}}, witness: []int{u}}
	}
	if s.out[u][v] {
		return naiveAddResult{err: ErrEdgeAlreadyExists}
	}
	if s.edges == s.e {
		return naiveAddResult{err: ErrEdgeLimit}
	}
	if s.ord[u] < s.ord[v] {
		s.out[u][v] = true
		s.in[v][u] = true
		s.edges++
		return naiveAddResult{moved: []int{}}
	}

	lb, ub := s.ord[v], s.ord[u]
	df, parent := s.forward(v, ub)
	if df[u] {
		witness := naivePath(parent, v, u)
		return naiveAddResult{err: &CycleError{Witness: witness}, witness: witness}
	}
	db := s.backward(u, lb)
	pool := make([]int, 0, len(df)+len(db))
	for x := range df {
		pool = append(pool, s.ord[x])
	}
	for x := range db {
		pool = append(pool, s.ord[x])
	}
	sort.Ints(pool)
	dfNodes, dbNodes := make([]int, 0, len(df)), make([]int, 0, len(db))
	for x := range df {
		dfNodes = append(dfNodes, x)
	}
	for x := range db {
		dbNodes = append(dbNodes, x)
	}
	s.sortByOrd(dfNodes)
	s.sortByOrd(dbNodes)
	sequence := append(dbNodes, dfNodes...)
	moved := []int{}
	for i, x := range sequence {
		old := s.ord[x]
		s.ord[x] = pool[i]
		if old != s.ord[x] {
			moved = append(moved, x)
		}
	}
	s.touched += len(sequence)
	s.sortByOrd(moved)
	s.out[u][v] = true
	s.in[v][u] = true
	s.edges++
	return naiveAddResult{moved: moved, forward: len(df), backward: len(db)}
}

func (s *naiveState) removeEdge(u, v int) error {
	if !s.live(u) || !s.live(v) {
		return ErrNodeNotFound
	}
	if !s.out[u][v] {
		return ErrEdgeNotFound
	}
	delete(s.out[u], v)
	delete(s.in[v], u)
	s.edges--
	return nil
}

func (s *naiveState) removeNode(x int) error {
	if !s.live(x) {
		return ErrNodeNotFound
	}
	for y := range s.out[x] {
		delete(s.in[y], x)
		s.edges--
	}
	for y := range s.in[x] {
		delete(s.out[y], x)
		s.edges--
	}
	s.out[x], s.in[x] = nil, nil
	s.alive[x] = false
	return nil
}

func (s *naiveState) order() []int {
	nodes := []int{}
	for x := 0; x < s.created; x++ {
		if s.alive[x] {
			nodes = append(nodes, x)
		}
	}
	s.sortByOrd(nodes)
	return nodes
}

func (s *naiveState) snapshot() *naiveState {
	cp := *s
	cp.alive = append([]bool(nil), s.alive...)
	cp.ord = append([]int(nil), s.ord...)
	cp.out = make([]map[int]bool, len(s.out))
	cp.in = make([]map[int]bool, len(s.in))
	for x := range s.out {
		if s.out[x] != nil {
			cp.out[x] = map[int]bool{}
			for y := range s.out[x] {
				cp.out[x][y] = true
			}
		}
		if s.in[x] != nil {
			cp.in[x] = map[int]bool{}
			for y := range s.in[x] {
				cp.in[x][y] = true
			}
		}
	}
	return &cp
}

func sameError(got, want error) bool {
	var gotCycle, wantCycle *CycleError
	gotIsCycle := errors.As(got, &gotCycle)
	wantIsCycle := errors.As(want, &wantCycle)
	if gotIsCycle != wantIsCycle {
		return false
	}
	if gotIsCycle {
		return reflect.DeepEqual(gotCycle.Witness, wantCycle.Witness)
	}
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func sameAddResult(got AddEdgeResult, err error, want naiveAddResult) bool {
	if !sameError(err, want.err) {
		return false
	}
	if want.err != nil {
		return true
	}
	return got.Forward == want.forward && got.Backward == want.backward &&
		((len(got.Moved) == 0 && len(want.moved) == 0) || reflect.DeepEqual(got.Moved, want.moved))
}

func sameBatchError(err error, index int, reason error, witness []int) bool {
	var batch *BatchError
	if !errors.As(err, &batch) || batch.Index != index {
		return false
	}
	var cycle *CycleError
	if errors.As(reason, &cycle) {
		return sameError(batch.Reason, &CycleError{Witness: witness})
	}
	return errors.Is(batch.Reason, reason)
}

func assertTopology(t *testing.T, m *Maintainer, step int, seed int64) {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	used := map[int]int{}
	for x := 0; x < m.created; x++ {
		if !m.alive[x] {
			continue
		}
		if y, ok := used[m.ord[x]]; ok {
			t.Fatalf("seed=%d step=%d duplicate ord %d for %d and %d", seed, step, m.ord[x], y, x)
		}
		used[m.ord[x]] = x
		for y := range m.out[x] {
			if m.ord[x] >= m.ord[y] {
				t.Fatalf("seed=%d step=%d edge %d->%d violates %d<%d", seed, step, x, y, m.ord[x], m.ord[y])
			}
		}
	}
}

func TestRandomSequencesAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(16)
		e := 1 + rng.Intn(32)
		m := NewMaintainer(n, e)
		naive := newNaive(n, e)
		steps := 30 + rng.Intn(41)

		check := func(step int, input, output, basis string) {
			t.Helper()
			t.Logf("seed=%d step=%d input=%s output=%s basis=%s order=%v touched=%d",
				seed, step, input, output, basis, m.Order(), m.Touched())
			if !reflect.DeepEqual(m.Order(), naive.order()) {
				t.Fatalf("seed=%d order mismatch got=%v want=%v", seed, m.Order(), naive.order())
			}
			if m.Touched() != naive.touched {
				t.Fatalf("seed=%d touched got=%d want=%d", seed, m.Touched(), naive.touched)
			}
			assertTopology(t, m, step, seed)
		}

		for step := 0; step < steps; step++ {
			switch rng.Intn(100) {
			case 0:
				u := rng.Intn(n)
				gotErr := m.RemoveNode(u)
				wantErr := naive.removeNode(u)
				check(step, fmt.Sprintf("RemoveNode(%d)", u), fmt.Sprintf("err=%v", gotErr), "existence; incident edges removed")
				if !sameError(gotErr, wantErr) {
					t.Fatalf("seed=%d RemoveNode got=%v want=%v", seed, gotErr, wantErr)
				}
			case 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19:
				x, err := m.AddNode()
				y, wantErr := naive.addNode()
				check(step, "AddNode()", fmt.Sprintf("node=%d err=%v", x, err), "creation limit; ordinal equals id")
				if x != y || !sameError(err, wantErr) {
					t.Fatalf("seed=%d AddNode got=(%d,%v) want=(%d,%v)", seed, x, err, y, wantErr)
				}
			case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54:
				u, v := rng.Intn(n), rng.Intn(n)
				got, err := m.AddEdge(u, v)
				want := naive.addEdge(u, v)
				check(step, fmt.Sprintf("AddEdge(%d,%d)", u, v),
					fmt.Sprintf("moved=%v forward=%d backward=%d err=%v", got.Moved, got.Forward, got.Backward, err),
					"validation order; delta B then delta F receives sorted pool")
				if !sameAddResult(got, err, want) {
					t.Fatalf("seed=%d AddEdge mismatch got=%+v,%v want=%+v,witness=%v", seed, got, err, want, want.witness)
				}
			case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69:
				batch := make([][2]int, 1+rng.Intn(3))
				labels := []string{}
				for i := range batch {
					batch[i] = [2]int{rng.Intn(n), rng.Intn(n)}
					labels = append(labels, fmt.Sprintf("%d->%d", batch[i][0], batch[i][1]))
				}
				saved := naive.snapshot()
				counts := []EdgeCount{}
				failIndex, failReason, witness := -1, error(nil), []int(nil)
				for i, edge := range batch {
					result := naive.addEdge(edge[0], edge[1])
					if result.err != nil {
						failIndex, failReason, witness = i, result.err, result.witness
						naive = saved
						break
					}
					counts = append(counts, EdgeCount{Forward: result.forward, Backward: result.backward})
				}
				got, err := m.AddEdges(batch)
				if failReason == nil {
					moved := []int{}
					for x := 0; x < naive.created; x++ {
						if naive.alive[x] && naive.ord[x] != saved.ord[x] {
							moved = append(moved, x)
						}
					}
					naive.sortByOrd(moved)
					check(step, fmt.Sprintf("AddEdges(%v)", labels),
						fmt.Sprintf("moved=%v counts=%+v err=%v", got.Moved, got.Counts, err),
						"sequential visibility; final ordinal difference only")
					if err != nil || !reflect.DeepEqual(got.Moved, moved) || !reflect.DeepEqual(got.Counts, counts) {
						t.Fatalf("seed=%d batch got=%+v,%v want moved=%v counts=%v", seed, got, err, moved, counts)
					}
				} else {
					check(step, fmt.Sprintf("AddEdges(%v)", labels),
						fmt.Sprintf("index=%d reason=%v witness=%v", failIndex, failReason, witness),
						"atomic rollback restores ordinals, edges, creation count and touched")
					if !sameBatchError(err, failIndex, failReason, witness) {
						t.Fatalf("seed=%d batch failure got=%v want index=%d reason=%v witness=%v",
							seed, err, failIndex, failReason, witness)
					}
				}
			default:
				u, v := rng.Intn(n), rng.Intn(n)
				gotErr := m.RemoveEdge(u, v)
				wantErr := naive.removeEdge(u, v)
				check(step, fmt.Sprintf("RemoveEdge(%d,%d)", u, v), fmt.Sprintf("err=%v", gotErr), "node then edge existence; ordinals unchanged")
				if !sameError(gotErr, wantErr) {
					t.Fatalf("seed=%d RemoveEdge got=%v want=%v", seed, gotErr, wantErr)
				}
			}
		}
	}
}
