package dbscan

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type opKind int

const (
	opInsert opKind = iota
	opRemove
	opTick
)

type randomOp struct {
	kind opKind
	id   int
	x, y int
	t    int64
}

func (op randomOp) String() string {
	switch op.kind {
	case opInsert:
		return fmt.Sprintf("Insert(%d,%d,%d)", op.id, op.x, op.y)
	case opRemove:
		return fmt.Sprintf("Remove(%d)", op.id)
	default:
		return fmt.Sprintf("Tick(%d)", op.t)
	}
}

// runOp applies one operation to the service.
func runOp(d *DBSCAN, op randomOp) (Result, error) {
	switch op.kind {
	case opInsert:
		return d.Insert(op.id, op.x, op.y)
	case opRemove:
		return d.Remove(op.id)
	default:
		return d.Tick(op.t)
	}
}

// genSequence produces one deterministic pseudo-random operation sequence.
func genSequence(rng *rand.Rand) (eps, minPts int, window int64, capacity int, ops []randomOp) {
	eps = 1 + rng.Intn(4)
	minPts = 1 + rng.Intn(4)
	window = int64(1 + rng.Intn(15))
	capacity = 4 + rng.Intn(16)

	centers := [][2]int{{0, 0}, {6, 0}, {-6, 3}, {0, -6}}
	now := int64(0)
	n := 30 + rng.Intn(70)
	for i := 0; i < n; i++ {
		switch r := rng.Intn(100); {
		case r < 45: // insert
			op := randomOp{kind: opInsert, id: 1 + rng.Intn(capacity+2)}
			c := centers[rng.Intn(len(centers))]
			op.x = c[0] + rng.Intn(7) - 3
			op.y = c[1] + rng.Intn(7) - 3
			if rng.Intn(20) == 0 { // occasionally invalid
				op.x = 1_000_001
			}
			if rng.Intn(25) == 0 {
				op.id = 0
			}
			ops = append(ops, op)
		case r < 70: // remove
			op := randomOp{kind: opRemove, id: 1 + rng.Intn(capacity+2)}
			if rng.Intn(25) == 0 {
				op.id = -1
			}
			ops = append(ops, op)
		default: // tick
			op := randomOp{kind: opTick}
			switch rng.Intn(12) {
			case 0:
				op.t = now - 1 // clock rollback (rejected once now > 0)
			case 1:
				op.t = 1_000_000_000_000_001 // too large
			default:
				now += int64(rng.Intn(int(2*window) + 1))
				op.t = now
			}
			ops = append(ops, op)
		}
	}
	return eps, minPts, window, capacity, ops
}

// checkState compares the full observable state of the service against the
// reference clustering.
func checkState(t *testing.T, d *DBSCAN, sim *naiveSim, st naiveState, maxID int) {
	t.Helper()
	if got, want := d.Alive(), len(sim.pts); got != want {
		t.Fatalf("Alive() = %d, want %d", got, want)
	}
	for id := 1; id <= maxID; id++ {
		want := -1
		if l, ok := st.labels[id]; ok {
			want = l
		}
		if got := d.Label(id); got != want {
			t.Fatalf("Label(%d) = %d, want %d", id, got, want)
		}
	}
	for id := range sim.pts {
		if got := d.Neighbors(id); !reflect.DeepEqual(got, st.nb[id]) {
			t.Fatalf("Neighbors(%d) = %v, want %v", id, got, st.nb[id])
		}
	}
	byLabel := map[int][]int{}
	for id, l := range st.labels {
		if l > 0 {
			byLabel[l] = append(byLabel[l], id)
		}
	}
	var want []Cluster
	for l, members := range byLabel {
		sort.Ints(members)
		want = append(want, Cluster{Label: l, Members: members})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Label < want[j].Label })
	if got := d.Clusters(); !reflect.DeepEqual(normalizeClusters(got), normalizeClusters(want)) {
		t.Fatalf("Clusters() = %+v, want %+v", got, want)
	}
}

func normalizeClusters(c []Cluster) []Cluster {
	if c == nil {
		return []Cluster{}
	}
	return c
}

// TestRandomAgainstNaive replays 2000 random operation sequences against
// both the incremental service and a whole-sale reclustering reference,
// comparing changes, events, and the full state after every operation.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < sequences; seq++ {
		eps, minPts, window, capacity, ops := genSequence(rng)
		d, err := New(eps, minPts, window, capacity)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		sim := newNaiveSim(eps, minPts, window, capacity)
		maxID := capacity + 2

		fail := func(step int, op randomOp, format string, args ...any) {
			t.Helper()
			t.Logf("sequence %d params: eps=%d minPts=%d W=%d C=%d", seq, eps, minPts, window, capacity)
			for i := 0; i <= step && i < len(ops); i++ {
				t.Logf("  op[%d] %s", i, ops[i])
			}
			t.Logf("mismatch at op[%d] %s:", step, op)
			t.Logf("  "+format, args...)
			t.FailNow()
		}

		for step, op := range ops {
			gotRes, gotErr := runOp(d, op)
			wantRes, wantErr := sim.apply(op)
			if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
				fail(step, op, "error mismatch: got %v, want %v (judged by errors.Is)", gotErr, wantErr)
			}
			if gotErr != nil {
				continue
			}
			if !reflect.DeepEqual(normalizeChanges(gotRes.Changes), normalizeChanges(wantRes.Changes)) {
				fail(step, op, "changes mismatch:\n  got  %+v\n  want %+v (diff of full reclusterings before/after)",
					gotRes.Changes, wantRes.Changes)
			}
			if !reflect.DeepEqual(normalizeEvents(gotRes.Events), normalizeEvents(wantRes.Events)) {
				fail(step, op, "events mismatch:\n  got  %+v\n  want %+v (bipartite diff of full reclusterings)",
					gotRes.Events, wantRes.Events)
			}
			st := sim.recluster()
			func() {
				defer func() {
					if r := recover(); r != nil {
						fail(step, op, "state mismatch: %v", r)
					}
				}()
				checkState(t, d, sim, st, maxID)
			}()
			if t.Failed() {
				return
			}
		}
	}
	t.Logf("replayed %d random sequences against the naive reference: all matched", sequences)
}

// TestReplayDeterminism runs the same sequences twice and requires
// bit-identical reports and final states.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for seq := 0; seq < 50; seq++ {
		eps, minPts, window, capacity, ops := genSequence(rng)
		var runs [][]Result
		var finalClusters []Cluster
		for run := 0; run < 2; run++ {
			d, err := New(eps, minPts, window, capacity)
			if err != nil {
				t.Fatalf("seq %d: New: %v", seq, err)
			}
			var results []Result
			for _, op := range ops {
				res, _ := runOp(d, op)
				results = append(results, Result{
					Changes: normalizeChanges(res.Changes),
					Events:  normalizeEvents(res.Events),
				})
			}
			runs = append(runs, results)
			got := normalizeClusters(d.Clusters())
			if run == 0 {
				finalClusters = got
			} else if !reflect.DeepEqual(got, finalClusters) {
				t.Fatalf("seq %d: replay produced different final clusters:\n%+v\n%+v", seq, finalClusters, got)
			}
		}
		if !reflect.DeepEqual(runs[0], runs[1]) {
			t.Fatalf("seq %d: replay produced different reports", seq)
		}
	}
}
