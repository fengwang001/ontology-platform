package reconcile

import (
	"bytes"
	"errors"
	"math/rand"
	"sort"
	"testing"
)

func TestNewReplicaValidation(t *testing.T) {
	cases := []struct {
		name   string
		size   int
		fanout int
		want   error
	}{
		{"zero size", 0, 2, ErrKeySpaceSize},
		{"negative size", -3, 2, ErrKeySpaceSize},
		{"fanout one", 8, 1, ErrInvalidFanout},
		{"fanout zero", 8, 0, ErrInvalidFanout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewReplica("r", tc.size, tc.fanout); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestPutGetDeleteValidation(t *testing.T) {
	r, err := NewReplica("r", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(-1, []byte{1}); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("negative key: %v", err)
	}
	if err := r.Put(4, []byte{1}); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("key == size: %v", err)
	}
	if err := r.Put(0, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("nil value: %v", err)
	}
	if err := r.Delete(9); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("delete out of range: %v", err)
	}
	if _, _, err := r.Get(-2); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("get out of range: %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("rejected ops must not mutate state, len=%d", r.Len())
	}

	if err := r.Put(1, []byte{}); err != nil {
		t.Fatalf("empty (zero) value must be accepted: %v", err)
	}
	if v, ok, err := r.Get(1); err != nil || !ok || v == nil || len(v) != 0 {
		t.Fatalf("zero-value key: v=%v ok=%v err=%v", v, ok, err)
	}
	if v, ok, err := r.Get(2); err != nil || ok || v != nil {
		t.Fatalf("absent key must differ from present zero value: v=%v ok=%v", v, ok)
	}

	if err := r.Delete(1); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.Get(1); ok {
		t.Fatal("delete failed")
	}
	if err := r.Delete(1); err != nil {
		t.Fatalf("deleting absent key should be a no-op, got %v", err)
	}

	// Put must copy its argument; later caller mutation must not change state.
	buf := []byte{1, 2, 3}
	if err := r.Put(0, buf); err != nil {
		t.Fatal(err)
	}
	buf[0] = 9
	if v, _, _ := r.Get(0); !bytes.Equal(v, []byte{1, 2, 3}) {
		t.Fatalf("Put did not copy value: %v", v)
	}
}

func TestTooManyKeys(t *testing.T) {
	_, err := NewReplicaFromData("big", 2, 2, map[int][]byte{
		0: {1}, 1: {2}, 2: {3},
	})
	if !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("want ErrTooManyKeys, got %v", err)
	}

	r, err := NewReplica("r", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(0, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(1, []byte{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(0, []byte{9}); err != nil {
		t.Fatalf("updating an existing key must not count as new: %v", err)
	}

	// Invalid seed data must not produce a replica at all.
	if _, err := NewReplicaFromData("bad", 2, 2, map[int][]byte{5: {1}}); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("seed out of range: %v", err)
	}
	if _, err := NewReplicaFromData("bad", 2, 2, map[int][]byte{0: nil}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("seed nil value: %v", err)
	}
}

func TestIncrementalHashMatchesRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		size := 1 + rng.Intn(50)
		fanout := 2 + rng.Intn(4)
		inc, _ := NewReplica("inc", size, fanout)
		data := map[int][]byte{}
		for _, key := range rng.Perm(size)[:rng.Intn(size+1)] {
			value := make([]byte, rng.Intn(4))
			rng.Read(value)
			if err := inc.Put(key, value); err != nil {
				t.Fatal(err)
			}
			stored := make([]byte, len(value))
			copy(stored, value)
			data[key] = stored
		}
		if rng.Intn(2) == 0 && len(data) > 0 {
			for key := range data {
				delete(data, key)
				if err := inc.Delete(key); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		fresh, err := NewReplicaFromData("fresh", size, fanout, data)
		if err != nil {
			t.Fatal(err)
		}
		inc.mu.RLock()
		is := inc.snapshotLocked()
		fresh.mu.RLock()
		fs := fresh.snapshotLocked()
		rootStride := ipow(fanout, treeDepth(size, fanout))
		ih := is.nodeHashFrom(treeDepth(size, fanout), 0, rootStride)
		fh := fs.nodeHashFrom(treeDepth(size, fanout), 0, rootStride)
		inc.mu.RUnlock()
		fresh.mu.RUnlock()
		if ih != fh {
			t.Fatalf("trial %d: incremental root digest %x != rebuilt %x", trial, ih, fh)
		}
	}
}

func TestZeroValueHashesDifferFromAbsent(t *testing.T) {
	size, fanout := 8, 2
	empty, _ := NewReplica("empty", size, fanout)
	zero, _ := NewReplica("zero", size, fanout)
	if err := zero.Put(3, []byte{}); err != nil {
		t.Fatal(err)
	}
	report, err := Reconcile(empty, zero)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diffs) != 1 || report.Diffs[0].Key != 3 {
		t.Fatalf("zero-value key must be a diff vs absent key: %+v", report.Diffs)
	}
	d := report.Diffs[0]
	if d.LeftOK || !d.RightOK || len(d.Right) != 0 {
		t.Fatalf("diff sides wrong: %+v", d)
	}
}

func TestBoundaryKeys(t *testing.T) {
	// size 8, fanout 2: boundaries are 0,2,4,6,8. Diff keys sit exactly on
	// split boundaries and at the key-space edge.
	left, _ := NewReplica("l", 8, 2)
	right, _ := NewReplica("r", 8, 2)
	for _, key := range []int{0, 2, 4, 6, 7} {
		if err := left.Put(key, []byte{byte(key)}); err != nil {
			t.Fatal(err)
		}
		if err := right.Put(key, []byte{byte(key + 100)}); err != nil {
			t.Fatal(err)
		}
	}
	// identical content at another boundary key
	left.Put(1, []byte("x"))
	right.Put(1, []byte("x"))

	report, err := Reconcile(left, right)
	if err != nil {
		t.Fatal(err)
	}
	var keys []int
	for _, d := range report.Diffs {
		keys = append(keys, d.Key)
	}
	sort.Ints(keys)
	if want := []int{0, 2, 4, 6, 7}; !intsEqual(keys, want) {
		t.Fatalf("diffs=%v want %v", keys, want)
	}

	// Deterministic comparison sequence: same pair reconciled twice must be
	// byte-for-byte identical.
	again, err := Reconcile(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Sequence) != len(report.Sequence) {
		t.Fatal("sequence length differs across runs")
	}
	for i := range again.Sequence {
		a, b := report.Sequence[i], again.Sequence[i]
		if a.Level != b.Level || a.Lo != b.Lo || a.Hi != b.Hi || a.Equal != b.Equal || a.DrilledDown != b.DrilledDown ||
			!bytes.Equal(a.LeftDigest, b.LeftDigest) || !bytes.Equal(a.RightDigest, b.RightDigest) {
			t.Fatalf("comparison %d not deterministic: %+v vs %+v", i, a, b)
		}
	}

	// Every mismatch at an internal node must have been drilled; equal nodes
	// never drilled.
	for _, c := range report.Sequence {
		if c.Equal && c.DrilledDown {
			t.Fatalf("equal interval was drilled: %+v", c)
		}
		if !c.Equal && c.Level > 0 && !c.DrilledDown {
			t.Fatalf("mismatched internal node not drilled: %+v", c)
		}
	}
}

func TestShapeMismatchAndNil(t *testing.T) {
	a, _ := NewReplica("a", 8, 2)
	b, _ := NewReplica("b", 8, 3)
	c, _ := NewReplica("c", 9, 2)
	if _, err := Reconcile(a, nil); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil replica: %v", err)
	}
	if _, err := Reconcile(nil, a); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil replica: %v", err)
	}
	if _, err := Reconcile(a, b); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("fanout mismatch: %v", err)
	}
	if _, err := Reconcile(a, c); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("size mismatch: %v", err)
	}
	if a.Len() != 0 || b.Len() != 0 {
		t.Fatal("rejected reconciliation must not mutate replicas")
	}
}

func intsEqual(a, b []int) bool {
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
