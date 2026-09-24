package semi

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"

	"ontology/key"
)

func batch(left map[int64]*string, ref map[string]int) []int64 {
	out := []int64{}
	for id, k := range left {
		if v, ok := key.Value(k); ok && ref[v] >= 1 {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func strptr(v string) *string { return &v }

// stp encodes a built-in op: L/l=Add/DelLeft, R/r=Add/DelRight.
type stp struct {
	op   byte
	id   int64
	k    *string
	want []int64
}

func apply(s *Semi, q stp) error {
	switch q.op {
	case 'L':
		return s.AddLeft(q.id, q.k)
	case 'l':
		return s.DelLeft(q.id)
	case 'R':
		return s.AddRight(q.k)
	}
	return s.DelRight(q.k)
}

// seq8 is the NOTES.md eight-step sequence with expected per-step views.
var seq8 = []stp{
	{'L', 1, strptr("a"), []int64{}},
	{'L', 2, strptr("a"), []int64{}},
	{'R', 0, strptr("a"), []int64{1, 2}},
	{'L', 3, strptr("b"), []int64{1, 2}},
	{'R', 0, strptr("b"), []int64{1, 2, 3}},
	{'L', 4, nil, []int64{1, 2, 3}},
	{'r', 0, strptr("a"), []int64{3}},
	{'R', 0, nil, []int64{3}},
}

// SelfCheck replays built-in sequences on fresh instances, verifying the
// four invariants, the indexed counter and concurrent readers.
func (s *Semi) SelfCheck() error {
	m, _ := New(8)
	for i, q := range seq8 {
		if err := apply(m, q); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if v := m.View(); !reflect.DeepEqual(v, q.want) || !reflect.DeepEqual(v, batch(m.left, m.ref)) {
			return fmt.Errorf("step %d view = %v", i+1, v)
		}
	}
	for _, seed := range []int64{1, 7, 42} {
		if err := randomCheck(rand.New(rand.NewSource(seed))); err != nil {
			return err
		}
	}
	for _, n := range []int{100, 1000, 10000} {
		t, _ := New(n + 1)
		for i := 0; i < n; i++ {
			v := fmt.Sprintf("k%d", i)
			if err := t.AddLeft(int64(i+1), &v); err != nil {
				return err
			}
		}
		if err := t.AddRight(strptr("k0")); err != nil || t.lastScan != 1 {
			return fmt.Errorf("m=%d add scan = %d", n, t.lastScan)
		}
		if err := t.DelRight(strptr("k0")); err != nil || t.lastScan != 1 {
			return fmt.Errorf("m=%d del scan = %d", n, t.lastScan)
		}
	}
	t, _ := New(64)
	for i := int64(1); i <= 32; i++ {
		v := fmt.Sprintf("k%d", i)
		if err := t.AddLeft(i, &v); err != nil || t.AddRight(&v) != nil {
			return err
		}
	}
	base := t.View()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				if !reflect.DeepEqual(t.View(), base) {
					panic("concurrent views differ")
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

func randomCheck(r *rand.Rand) error {
	const N = 300
	s, _ := New(N)
	left, ref := map[int64]*string{}, map[string]int{}
	pick := func() *string {
		if r.Intn(5) == 0 {
			return nil
		}
		v := []string{"a", "b", "c", ""}[r.Intn(4)]
		return &v
	}
	for i := 0; i < 1500; i++ {
		id, k, before := int64(r.Intn(N)), pick(), s.View()
		var err error
		switch x := r.Intn(5); x {
		case 0:
			if err = s.AddLeft(id, k); err == nil {
				left[id] = k
			}
		case 1:
			if err = s.DelLeft(id); err == nil {
				delete(left, id)
			}
		case 2, 3:
			if err = s.AddRight(k); err == nil && k != nil {
				ref[*k]++
			}
		default:
			if err = s.DelRight(k); err == nil && k != nil {
				ref[*k]--
			}
		}
		if v := s.View(); !reflect.DeepEqual(v, batch(left, ref)) || (err != nil && !reflect.DeepEqual(v, before)) {
			return fmt.Errorf("random op %d: %v (rejected left a trace)", i, v)
		}
	}
	return nil
}
