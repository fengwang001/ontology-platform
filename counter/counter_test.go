package counter

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

type stepLogger struct{ t *testing.T }

func (l stepLogger) logf(format string, args ...any) {
	l.t.Helper()
	l.t.Logf(format, args...)
}

func values(t *testing.T, c *Cluster, tag string) []*big.Int {
	t.Helper()
	vs := make([]*big.Int, c.Size())
	parts := make([]string, c.Size())
	for i := range vs {
		v, err := c.Value(i)
		if err != nil {
			t.Fatalf("Value(%d) unexpected error: %v", i, err)
		}
		vs[i] = v
		parts[i] = fmt.Sprintf("r%d=%d", i, v)
	}
	t.Logf("[%s] values: %v", tag, parts)
	return vs
}

// NaiveModel replays the same operations with plain vectors; convergence
// results are compared against it.
type NaiveModel struct {
	mu sync.Mutex
	n  int
	p  [][]int64
	d  [][]int64
}

func NewNaiveModel(n int) *NaiveModel {
	m := &NaiveModel{n: n}
	m.p = make([][]int64, n)
	m.d = make([][]int64, n)
	for i := range m.p {
		m.p[i] = make([]int64, n)
		m.d[i] = make([]int64, n)
	}
	return m
}

func (m *NaiveModel) Update(id int, delta int64, inc bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inc {
		m.p[id][id] += delta
	} else {
		m.d[id][id] += delta
	}
}

func (m *NaiveModel) Merge(dst, src int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < m.n; i++ {
		if m.p[src][i] > m.p[dst][i] {
			m.p[dst][i] = m.p[src][i]
		}
		if m.d[src][i] > m.d[dst][i] {
			m.d[dst][i] = m.d[src][i]
		}
	}
}

func (m *NaiveModel) mergeAllRound() {
	for i := 0; i < m.n; i++ {
		for j := 0; j < m.n; j++ {
			if i != j {
				m.Merge(i, j)
			}
		}
	}
}

func (m *NaiveModel) snapshotSums() []int64 {
	out := make([]int64, m.n)
	for r := 0; r < m.n; r++ {
		var s int64
		for i := 0; i < m.n; i++ {
			s += m.p[r][i] - m.d[r][i]
		}
		out[r] = s
	}
	return out
}

func (m *NaiveModel) ConvergedValue() int64 {
	prev := m.snapshotSums()
	for {
		m.mergeAllRound()
		cur := m.snapshotSums()
		if equalSlices(prev, cur) {
			return cur[0]
		}
		prev = cur
	}
}

func equalSlices(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapEqual(a, b *Snapshot) bool {
	if a == nil || b == nil {
		return a == b
	}
	pa, pb := a.P(), b.P()
	na, nb := a.N(), b.N()
	for i := range pa {
		if pa[i].Cmp(pb[i]) != 0 || na[i].Cmp(nb[i]) != 0 {
			return false
		}
	}
	return true
}

func snapSum(s *Snapshot) *big.Int { return s.Value() }

func TestBasicIncrementDecrementMerge(t *testing.T) {
	l := stepLogger{t}
	c, err := NewCluster(3)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}
	model := NewNaiveModel(3)

	ops := []struct {
		id    int
		delta int64
		inc   bool
	}{
		{0, 10, true},
		{1, 7, true},
		{2, 3, false},
		{0, 4, false},
		{2, 11, true},
	}
	for _, op := range ops {
		kind := "INC"
		if !op.inc {
			kind = "DEC"
		}
		l.logf("step %s r%d delta=%d", kind, op.id, op.delta)
		if op.inc {
			if err := c.Inc(op.id, uint64(op.delta)); err != nil {
				t.Fatalf("Inc: %v", err)
			}
		} else if err := c.Dec(op.id, uint64(op.delta)); err != nil {
			t.Fatalf("Dec: %v", err)
		}
		model.Update(op.id, op.delta, op.inc)
	}
	values(t, c, "local-only")

	merges := [][2]int{{0, 1}, {0, 2}, {1, 0}, {2, 0}, {2, 1}}
	for _, mg := range merges {
		l.logf("step MERGE r%d<-r%d", mg[0], mg[1])
		if err := c.Merge(mg[0], mg[1]); err != nil {
			t.Fatalf("Merge: %v", err)
		}
		model.Merge(mg[0], mg[1])
	}

	got := values(t, c, "after-gossip")
	want := model.ConvergedValue()
	for i, v := range got {
		if v.Int64() != want {
			t.Fatalf("replica %d value %d != naive reference %d", i, v, want)
		}
	}
	l.logf("convergence OK: all replicas = %d, matches naive reference %d (criterion: component-wise max union)", got[0], want)

	for i := 0; i < 3; i++ {
		if err := c.SelfCheck(i); err != nil {
			t.Fatalf("SelfCheck(%d): %v", i, err)
		}
	}
}

func TestNegativeValueAllowed(t *testing.T) {
	c, _ := NewCluster(2)
	if err := c.Dec(0, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(1, 0); err != nil {
		t.Fatal(err)
	}
	v, _ := c.Value(1)
	if v.Sign() >= 0 {
		t.Fatalf("negative counter must be preserved, got %d", v)
	}
	t.Logf("negative value allowed: r1=%d (neither rejected nor truncated)", v)
}

func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	l := stepLogger{t}
	c, _ := NewCluster(2)
	if err := c.Inc(0, 100); err != nil {
		t.Fatal(err)
	}
	before, _ := c.SnapshotAt(0)

	type tc struct {
		name string
		call func() error
		want error
	}
	cases := []tc{
		{"invalid replica id: Inc(7,1)", func() error { return c.Inc(7, 1) }, ErrInvalidReplica},
		{"invalid replica id: Dec(-1,1)", func() error { return c.Dec(-1, 1) }, ErrInvalidReplica},
		{"invalid replica id: Merge(0,9)", func() error { return c.Merge(0, 9) }, ErrInvalidReplica},
		{"invalid replica id: Value(2)", func() error { _, e := c.Value(2); return e }, ErrInvalidReplica},
		{"invalid replica id: SnapshotAt(2)", func() error { _, e := c.SnapshotAt(2); return e }, ErrInvalidReplica},
		{"non-positive delta: Inc(0,0)", func() error { return c.Inc(0, 0) }, ErrNonPositiveDelta},
		{"non-positive delta: Dec(0,0)", func() error { return c.Dec(0, 0) }, ErrNonPositiveDelta},
		{"component over limit", func() error { return c.Inc(0, MaxComponent) }, ErrComponentLimit},
		{"nil snapshot", func() error { return c.InstallSnapshot(0, nil) }, ErrNilSnapshot},
		{"snapshot width mismatch", func() error {
			other, _ := NewCluster(3)
			snap, _ := other.SnapshotAt(0)
			return c.InstallSnapshot(0, snap)
		}, ErrSnapshotMismatch},
		{"invalid cluster size", func() error { _, e := NewCluster(0); return e }, ErrInvalidCluster},
		{"corrupt snapshot payload", func() error {
			bad := &Snapshot{id: 0, p: []*big.Int{big.NewInt(-1)}, n: []*big.Int{big.NewInt(0)}}
			return c.InstallSnapshot(0, bad)
		}, ErrCorruptSnapshot},
	}
	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		l.logf("reject %-38s reason=%q [no state change]", tc.name, err.Error())
	}

	distinct := map[string]bool{}
	for _, tc := range cases {
		distinct[tc.want.Error()] = true
	}
	if len(distinct) != 7 {
		t.Fatalf("expected 7 distinct error reasons, got %d", len(distinct))
	}

	after, _ := c.SnapshotAt(0)
	if !snapEqual(before, after) {
		t.Fatalf("rejected operations mutated state: before=%v after=%v", before.P(), after.P())
	}
	l.logf("all %d rejections across 7 distinct categories; state bit-for-bit unchanged: r0=%s", len(cases), snapSum(after))
}

func TestLimitBoundaryExactly(t *testing.T) {
	c, _ := NewCluster(1)
	if err := c.Inc(0, MaxComponent-1); err != nil {
		t.Fatal(err)
	}
	if err := c.Inc(0, 1); !errors.Is(err, ErrComponentLimit) {
		t.Fatalf("got %v, want ErrComponentLimit", err)
	}
	v, _ := c.Value(0)
	if v.Uint64() != MaxComponent-1 {
		t.Fatalf("boundary component changed by rejected call: %d", v)
	}
	t.Logf("boundary: component=%d (< MaxComponent=%d), +1 rejected with no trace", v, MaxComponent)
}

func TestCRDTLaws(t *testing.T) {
	l := stepLogger{t}

	// Idempotence.
	c, _ := NewCluster(2)
	_ = c.Inc(0, 5)
	_ = c.Dec(1, 2)
	if err := c.Merge(0, 0); err != nil {
		t.Fatalf("self-merge must be a legal no-op, got %v", err)
	}
	s1, _ := c.SnapshotAt(0)
	if err := c.Merge(0, 1); err != nil {
		t.Fatal(err)
	}
	s2, _ := c.SnapshotAt(0)
	if err := c.Merge(0, 1); err != nil {
		t.Fatal(err)
	}
	s3, _ := c.SnapshotAt(0)
	_ = s1
	if !snapEqual(s2, s3) {
		t.Fatal("idempotence: second identical merge changed state")
	}
	l.logf("idempotence: self-merge legal no-op; repeated merge(0<-1) identical: %s", snapSum(s3))

	// Commutativity.
	build := func() *Cluster {
		cc, _ := NewCluster(2)
		_ = cc.Inc(0, 9)
		_ = cc.Dec(0, 1)
		_ = cc.Inc(1, 4)
		_ = cc.Dec(1, 6)
		return cc
	}
	orderAB := build()
	_ = orderAB.Merge(0, 1)
	_ = orderAB.Merge(1, 0)
	orderBA := build()
	_ = orderBA.Merge(1, 0)
	_ = orderBA.Merge(0, 1)
	for i := 0; i < 2; i++ {
		a, _ := orderAB.SnapshotAt(i)
		b, _ := orderBA.SnapshotAt(i)
		if !snapEqual(a, b) {
			t.Fatalf("commutativity violated at r%d", i)
		}
	}
	v0, _ := orderAB.Value(0)
	v1, _ := orderAB.Value(1)
	if v0.Cmp(v1) != 0 {
		t.Fatal("both replicas must agree after cross merge")
	}
	l.logf("commutativity: opposite merge orders yield identical states, both=%d", v0)

	// Associativity.
	setup3 := func() *Cluster {
		cc, _ := NewCluster(3)
		_ = cc.Inc(0, 3)
		_ = cc.Inc(1, 5)
		_ = cc.Dec(2, 2)
		_ = cc.Inc(2, 7)
		return cc
	}
	left := setup3()
	_ = left.Merge(0, 1)
	_ = left.Merge(0, 2)
	right := setup3()
	_ = right.Merge(1, 2)
	_ = right.Merge(0, 1)
	sl, _ := left.SnapshotAt(0)
	sr, _ := right.SnapshotAt(0)
	if !snapEqual(sl, sr) {
		t.Fatalf("associativity violated: (r0<-r1)<-r2=%s != r0<-(r1<-r2)=%s", snapSum(sl), snapSum(sr))
	}
	l.logf("associativity: (r0<-r1)<-r2 == r0<-(r1<-r2) == %s", snapSum(sl))

	// Delivered twice / out of order: same convergence.
	dup := setup3()
	_ = dup.Merge(0, 1)
	_ = dup.Merge(0, 1) // duplicate delivery
	_ = dup.Merge(0, 2)
	_ = dup.Merge(1, 0)
	_ = dup.Merge(2, 0)
	_ = dup.Merge(2, 1)
	_ = dup.Merge(1, 2)
	got := values(t, dup, "duplicate+unordered delivery")
	model := NewNaiveModel(3)
	model.Update(0, 3, true)
	model.Update(1, 5, true)
	model.Update(2, 2, false)
	model.Update(2, 7, true)
	want := model.ConvergedValue()
	for i, v := range got {
		if v.Int64() != want {
			t.Fatalf("r%d=%d, want naive reference %d", i, v, want)
		}
	}
	l.logf("duplicate/out-of-order merges converge to %d, matching naive reference", want)
}

func TestConcurrentInterleavingAndConvergence(t *testing.T) {
	l := stepLogger{t}
	const n = 4
	c, err := NewCluster(n)
	if err != nil {
		t.Fatal(err)
	}
	model := NewNaiveModel(n)

	// Planned local operations per replica.
	plan := [][]struct {
		delta uint64
		inc   bool
	}{
		{{10, true}, {3, false}, {2, true}},
		{{7, true}, {7, false}, {1, true}},
		{{20, true}, {5, false}},
		{{4, false}, {9, true}, {1, false}},
	}

	var wg sync.WaitGroup

	// Local inc/dec workers.
	for r := 0; r < n; r++ {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, op := range plan[r] {
				if op.inc {
					if err := c.Inc(r, op.delta); err != nil {
						t.Errorf("Inc r%d: %v", r, err)
						return
					}
				} else if err := c.Dec(r, op.delta); err != nil {
					t.Errorf("Dec r%d: %v", r, err)
					return
				}
			}
		}()
	}

	// Gossip workers: every ordered pair is merged repeatedly, including
	// reciprocal directions a<-b and b<-a running at the same time.
	for round := 0; round < 3; round++ {
		for a := 0; a < n; a++ {
			for b := 0; b < n; b++ {
				a, b := a, b
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := c.Merge(a, b); err != nil {
						t.Errorf("Merge %d<-%d: %v", a, b, err)
					}
				}()
			}
		}
	}

	// Reader/self-check workers exercising concurrent reads.
	for r := 0; r < n; r++ {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				if _, err := c.Value(r); err != nil {
					t.Errorf("Value r%d: %v", r, err)
					return
				}
				if _, err := c.SnapshotAt(r); err != nil {
					t.Errorf("SnapshotAt r%d: %v", r, err)
					return
				}
				if err := c.SelfCheck(r); err != nil {
					t.Errorf("SelfCheck r%d: %v", r, err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// Reference expected total.
	for r := range plan {
		for _, op := range plan[r] {
			model.Update(r, int64(op.delta), op.inc)
		}
	}

	// Deterministic final merge phase: gossip until fixpoint.
	prev := values(t, c, "after concurrent phase")
	for {
		for a := 0; a < n; a++ {
			for b := 0; b < n; b++ {
				if err := c.Merge(a, b); err != nil {
					t.Fatal(err)
				}
			}
		}
		cur := values(t, c, "merge round")
		same := true
		for i := range cur {
			if cur[i].Cmp(prev[i]) != 0 {
				same = false
			}
		}
		if same {
			break
		}
		prev = cur
	}

	want := model.ConvergedValue()
	final := values(t, c, "converged")
	for i, v := range final {
		if v.Int64() != want {
			t.Fatalf("r%d converged value %d != naive reference %d (criterion: gossip fixpoint equals reference)", i, v, want)
		}
		if err := c.SelfCheck(i); err != nil {
			t.Fatalf("SelfCheck r%d: %v", i, err)
		}
	}
	l.logf("concurrent interleavings + reciprocal merges: no deadlock, all replicas converge to %d", want)
}

func TestSnapshotRoundTrip(t *testing.T) {
	l := stepLogger{t}
	c, _ := NewCluster(2)
	_ = c.Inc(0, 8)
	_ = c.Dec(0, 3)
	_ = c.Inc(1, 2)

	snap, err := c.SnapshotAt(0)
	if err != nil {
		t.Fatal(err)
	}
	l.logf("snapshot r0: P=%v N=%v value=%d", snap.P(), snap.N(), snap.Value())

	if err := c.Merge(1, 0); err != nil {
		t.Fatal(err)
	}
	// Mutating a detached snapshot must not affect cluster state.
	snap.P()[0].SetInt64(999_999)
	v, _ := c.Value(0)
	if v.Int64() != 5 {
		t.Fatalf("snapshot mutation leaked into cluster: r0=%d", v)
	}

	// Install the detached snapshot into r1: installs its recorded state.
	if err := c.InstallSnapshot(1, snap); err != nil {
		t.Fatal(err)
	}
	v1, _ := c.Value(1)
	if v1.Int64() != 999_999 {
		t.Fatalf("install snapshot: r1=%d", v1)
	}
	// r0 stays untouched.
	v0, _ := c.Value(0)
	if v0.Int64() != 5 {
		t.Fatalf("install leaked into other replica: r0=%d", v0)
	}
	l.logf("snapshot detached (mutations isolated); install r1=%d while r0=%d", v1, v0)
}
