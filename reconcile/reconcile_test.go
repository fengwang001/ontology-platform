package reconcile

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/reconcile/internal/logger"
)

func mustReplica(t *testing.T, fanout, keyMax uint64, maxKeys int) *Replica {
	t.Helper()
	r, err := NewReplica(fanout, keyMax, maxKeys)
	if err != nil {
		t.Fatalf("NewReplica: %v", err)
	}
	return r
}

func digestSet(t *testing.T, r *Replica) map[[32]byte]struct{} {
	t.Helper()
	s := r.Snapshot()
	set := make(map[[32]byte]struct{})
	for _, d := range s.tree.levels[s.tree.depth-1] {
		set[d] = struct{}{}
	}
	return set
}

func TestBoundaryKeys(t *testing.T) {
	const keyMax uint64 = 8
	const fanout uint64 = 2
	a := mustReplica(t, fanout, keyMax, int(keyMax)+1)
	b := mustReplica(t, fanout, keyMax, int(keyMax)+1)

	for k := uint64(0); k <= keyMax; k++ {
		if err := a.Put(k, 100+k); err != nil {
			t.Fatalf("put a %d: %v", k, err)
		}
		if err := b.Put(k, 100+k); err != nil {
			t.Fatalf("put b %d: %v", k, err)
		}
	}
	report, err := Reconcile(a.Snapshot(), b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DiffKeys) != 0 {
		t.Fatalf("identical replicas reported diffs: %v", report.DiffKeys)
	}
	if len(report.Comparisons) != 1 || !report.Comparisons[0].HashesEq {
		t.Fatalf("root hash should match and stop, got %+v", report.Comparisons)
	}

	for _, k := range []uint64{0, 4, keyMax} {
		if err := b.Put(k, 999); err != nil {
			t.Fatalf("put b %d: %v", k, err)
		}
	}
	report, err = Reconcile(a.Snapshot(), b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{0, 4, keyMax}
	if fmt.Sprint(report.DiffKeys) != fmt.Sprint(want) {
		t.Fatalf("diff keys = %v, want %v", report.DiffKeys, want)
	}

	for _, c := range report.Comparisons {
		if c.Lo > c.Hi {
			t.Fatalf("interval not half-open valid: %+v", c)
		}
		if c.HashesEq && c.Level < a.tree.depth-1 {
			drilled := false
			for _, d := range report.Comparisons {
				if d.Level == c.Level+1 && d.Lo >= c.Lo && d.Hi <= c.Hi {
					drilled = true
				}
			}
			if drilled {
				t.Fatalf("matched interval %+v was drilled into", c)
			}
		}
	}
}

func TestZeroValueVsAbsent(t *testing.T) {
	a := mustReplica(t, 3, 10, 11)
	b := mustReplica(t, 3, 10, 11)
	if err := a.Put(7, 0); err != nil {
		t.Fatal(err)
	}

	if _, ok := a.Snapshot().Get(7); !ok {
		t.Fatal("present zero-valued key must report exists")
	}
	if _, ok := b.Snapshot().Get(7); ok {
		t.Fatal("absent key must report not exists")
	}

	report, err := Reconcile(a.Snapshot(), b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DiffKeys) != 1 || report.DiffKeys[0] != 7 {
		t.Fatalf("present-zero vs absent must be a diff, got %v", report.DiffKeys)
	}

	if err := b.Put(7, 0); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(a.Snapshot(), b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DiffKeys) != 0 {
		t.Fatalf("both zero-valued must match, got %v", report.DiffKeys)
	}

	empty := hashLeaf(false, 7, 0)
	zero := hashLeaf(true, 7, 0)
	nonzero := hashLeaf(true, 7, 42)
	if empty == zero || zero == nonzero || empty == nonzero {
		t.Fatal("absent / zero / non-zero leaf digests must be distinct")
	}
}

func TestIncrementalHashMaintenance(t *testing.T) {
	r := mustReplica(t, 2, 15, 16)
	initial := digestSet(t, r)
	if err := r.Put(5, 10); err != nil {
		t.Fatal(err)
	}
	afterPut := digestSet(t, r)
	if _, changed := afterPut[hashLeaf(true, 5, 10)]; !changed {
		t.Fatal("leaf digest not updated after put")
	}
	if _, stillEmpty := afterPut[hashLeaf(false, 5, 0)]; stillEmpty {
		t.Fatal("old absent digest still present after put")
	}
	if err := r.Put(5, 20); err != nil {
		t.Fatal(err)
	}
	if _, changed := digestSet(t, r)[hashLeaf(true, 5, 20)]; !changed {
		t.Fatal("leaf digest not updated after overwrite")
	}
	if err := r.Delete(5); err != nil {
		t.Fatal(err)
	}
	afterDelete := digestSet(t, r)
	if len(afterDelete) != len(initial) {
		t.Fatalf("after delete digest set = %d leaves, want %d", len(afterDelete), len(initial))
	}
	for d := range initial {
		if _, ok := afterDelete[d]; !ok {
			t.Fatal("digest set not restored after delete")
		}
	}

	full := mustReplica(t, 2, 7, 8)
	for k := uint64(0); k < 8; k++ {
		if err := full.Put(k, k); err != nil {
			t.Fatal(err)
		}
	}
	reb := mustReplica(t, 2, 7, 8)
	for k := uint64(7); ; k-- {
		if err := reb.Put(k, k); err != nil {
			t.Fatal(err)
		}
		if k == 0 {
			break
		}
	}
	if full.Snapshot().RootHash() != reb.Snapshot().RootHash() {
		t.Fatal("root hash must be independent of write order")
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name    string
		fanout  uint64
		keyMax  uint64
		maxKeys int
		want    error
	}{
		{"fanout one", 1, 10, 11, ErrInvalidFanout},
		{"fanout zero", 0, 10, 11, ErrInvalidFanout},
		{"key max zero", 2, 0, 1, ErrInvalidKeyMax},
		{"max keys zero", 2, 10, 0, ErrInvalidMaxKeys},
		{"max keys negative", 2, 10, -1, ErrInvalidMaxKeys},
		{"max keys larger than space", 2, 10, 12, ErrInvalidMaxKeys},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReplica(tc.fanout, tc.keyMax, tc.maxKeys)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	r := mustReplica(t, 2, 9, 3)
	if err := r.Put(10, 1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("out of range put: %v", err)
	}
	if err := r.Delete(10); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("out of range delete: %v", err)
	}
	if _, _, err := r.Get(10); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("out of range get: %v", err)
	}

	for _, k := range []uint64{0, 1, 2} {
		if err := r.Put(k, 1); err != nil {
			t.Fatal(err)
		}
	}
	err := r.Put(3, 1)
	if !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("over capacity: %v", err)
	}
	if r.Len() != 3 {
		t.Fatalf("rejected put changed length: %d", r.Len())
	}
	if err := r.Put(2, 999); err != nil {
		t.Fatalf("overwrite must not hit capacity: %v", err)
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	r := mustReplica(t, 2, 7, 2)
	if err := r.Put(1, 11); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(0, 1); err != nil {
		t.Fatal(err)
	}
	before := r.Snapshot()

	if err := r.Put(99, 1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("want ErrKeyOutOfRange, got %v", err)
	}
	if err := r.Put(2, 1); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("want ErrTooManyKeys, got %v", err)
	}
	if err := r.Delete(8); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("want ErrKeyOutOfRange, got %v", err)
	}

	after := r.Snapshot()
	if after.RootHash() != before.RootHash() || after.Len() != before.Len() {
		t.Fatal("rejected operations changed keys or range hashes")
	}
}

func TestShapeMismatch(t *testing.T) {
	r1 := mustReplica(t, 2, 15, 16)
	r2 := mustReplica(t, 3, 15, 16)
	r3 := mustReplica(t, 2, 16, 17)
	if _, err := Reconcile(r1.Snapshot(), r2.Snapshot()); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("fanout mismatch: %v", err)
	}
	if _, err := Reconcile(r1.Snapshot(), r3.Snapshot()); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("key space mismatch: %v", err)
	}
	if _, err := Reconcile(r1.Snapshot(), nil); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("nil snapshot: %v", err)
	}

	if err := r2.Put(0, 1); err != nil {
		t.Fatal(err)
	}
	if r2.Len() != 1 {
		t.Fatal("failed reconcile must not mutate replicas")
	}
}

func bruteForceDiff(a, b map[uint64]uint64) []uint64 {
	seen := make(map[uint64]struct{})
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			seen[k] = struct{}{}
		}
	}
	for k, v := range b {
		if av, ok := a[k]; !ok || av != v {
			seen[k] = struct{}{}
		}
	}
	keys := make([]uint64, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func TestRandomReconcileMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const keyMax uint64 = 255
	const fanout uint64 = 4
	for iter := 0; iter < 200; iter++ {
		a := mustReplica(t, fanout, keyMax, int(keyMax)+1)
		b := mustReplica(t, fanout, keyMax, int(keyMax)+1)
		ma := make(map[uint64]uint64)
		mb := make(map[uint64]uint64)

		fill := func(r *Replica, m map[uint64]uint64) {
			n := rng.Intn(int(keyMax) + 1)
			for i := 0; i < n; i++ {
				k := uint64(rng.Intn(int(keyMax) + 1))
				v := rng.Uint64()
				if rng.Intn(5) == 0 {
					v = 0
				}
				if err := r.Put(k, v); err != nil {
					t.Fatalf("iter %d put: %v", iter, err)
				}
				m[k] = v
			}
		}
		fill(a, ma)
		fill(b, mb)

		sa, sb := a.Snapshot(), b.Snapshot()
		report, err := Reconcile(sa, sb)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		got := append([]uint64(nil), report.DiffKeys...)
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		want := bruteForceDiff(ma, mb)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("iter %d: diff %v, want %v", iter, got, want)
		}

		// No diff key may be counted twice and every diff must be covered.
		covered := make(map[uint64]bool)
		for _, k := range report.DiffKeys {
			if covered[k] {
				t.Fatalf("iter %d: key %d reported twice", iter, k)
			}
			covered[k] = true
		}
		for _, k := range want {
			if !covered[k] {
				t.Fatalf("iter %d: key %d missed", iter, k)
			}
		}

		// Every mismatch is explained by an unbroken root-to-leaf chain.
		for _, k := range want {
			foundLeaf := false
			for _, c := range report.Comparisons {
				if c.Level == sa.tree.depth-1 && c.Lo == k && !c.HashesEq {
					foundLeaf = true
				}
			}
			if !foundLeaf {
				t.Fatalf("iter %d: diff key %d lacks a mismatching leaf comparison", iter, k)
			}
		}
	}
}

func TestDeterministicRepeatedReconcile(t *testing.T) {
	a := mustReplica(t, 3, 80, 81)
	b := mustReplica(t, 3, 80, 81)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 40; i++ {
		_ = a.Put(uint64(rng.Intn(81)), rng.Uint64())
		_ = b.Put(uint64(rng.Intn(81)), rng.Uint64())
	}
	sa, sb := a.Snapshot(), b.Snapshot()
	first, err := Reconcile(sa, sb)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		r, err := Reconcile(sa, sb)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(r.DiffKeys) != fmt.Sprint(first.DiffKeys) ||
			fmt.Sprint(r.Comparisons) != fmt.Sprint(first.Comparisons) {
			t.Fatalf("reconciliation not deterministic on run %d", i)
		}
	}
}

func TestConcurrentSnapshotConsistency(t *testing.T) {
	r := mustReplica(t, 2, 63, 64)
	for k := uint64(0); k < 30; k++ {
		if err := r.Put(k, k); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for k := uint64(0); k < 30; k++ {
					_ = r.Put(k, k)
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := r.Snapshot()
				for k, v := range s.values {
					if got, ok := s.Get(k); !ok || got != v {
						t.Errorf("snapshot field inconsistency for key %d", k)
					}
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		s := r.Snapshot()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				rep, err := Reconcile(s, r.Snapshot())
				if err != nil {
					t.Errorf("reconcile: %v", err)
				}
				for _, k := range rep.DiffKeys {
					if k > 63 {
						t.Errorf("diff key out of range: %d", k)
					}
				}
				i++
				if i >= 200 {
					return
				}
			}
		}
	}()

	// Run a bounded mix of readers and one writer, then stop and wait.
	close(stop)
	wg.Wait()
}

func TestReconcileLogsInputsDiffsAndBasis(t *testing.T) {
	var buf bytes.Buffer
	restore := logger.Set(logger.NewTextHandler(&buf, slog.LevelDebug))
	defer restore()

	a := mustReplica(t, 2, 7, 8)
	b := mustReplica(t, 2, 7, 8)
	if err := a.Put(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(3, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(3, 0); err != nil {
		t.Fatal(err)
	}

	report, err := Reconcile(a.Snapshot(), b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"reconcile start", "fanout=2", "key_max=7",
		"keys_a=2", "keys_b=1", "diff key at leaf", "key=1",
		"basis=\"leaf hashes differ\"", "combined hashes equal",
		"reconcile done",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q in:\n%s", want, out)
		}
	}
	if len(report.DiffKeys) != 1 || report.DiffKeys[0] != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}
