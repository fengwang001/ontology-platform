package semi

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"ontology/key"
)

func must(t *testing.T, e error) {
	if e != nil {
		t.Fatal(e)
	}
}
func ck(t *testing.T, g, w []int64, m string) {
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: %v want %v", m, g, w)
	}
}
func TestViewMatchesBatch(t *testing.T) {
	seqs := [][]stp{seq8, {
		{'R', 0, strptr("a"), []int64{}}, {'L', 1, strptr("a"), []int64{1}},
		{'L', 2, strptr("a"), []int64{1, 2}}, {'r', 0, strptr("a"), []int64{}},
	}}
	for si, seq := range seqs {
		s, _ := New(8)
		for i, q := range seq {
			must(t, apply(s, q))
			v := s.View()
			if !reflect.DeepEqual(v, q.want) || !reflect.DeepEqual(v, batch(s.left, s.ref)) {
				t.Fatalf("s%d st%d: %v want %v", si, i+1, v, q.want)
			}
		}
	}
}
func TestNoDuplicationWhenRefMultiplied(t *testing.T) {
	s, _ := New(8)
	must(t, s.AddLeft(1, strptr("a")))
	must(t, s.AddLeft(2, strptr("a")))
	for i := 1; i <= 3; i++ {
		must(t, s.AddRight(strptr("a")))
		ck(t, s.View(), []int64{1, 2}, "ref>1")
	}
	must(t, s.AddLeft(3, strptr("a")))
	ck(t, s.View(), []int64{1, 2, 3}, "late insert")
}
func TestRefBoundsAndNull(t *testing.T) {
	s, _ := New(8)
	if !errors.Is(s.DelRight(strptr("a")), ErrRightUnderflow) ||
		!errors.Is(s.DelRight(nil), ErrRightUnderflow) {
		t.Fatal("withdraw from zero must underflow")
	}
	must(t, s.AddLeft(1, nil))
	if e := s.AddRight(nil); e != nil || len(s.View()) != 0 {
		t.Fatal("NULL right lit a row")
	}
	if s.AddRight(strptr("a")) != nil || len(s.View()) != 0 {
		t.Fatal("NULL row matched by key")
	}
	must(t, s.DelRight(strptr("a")))
	if !errors.Is(s.DelRight(strptr("a")), ErrRightUnderflow) {
		t.Fatal("second withdraw must underflow")
	}
	must(t, s.AddRight(strptr("a")))
	ck(t, s.View(), []int64{}, "trace after rejected withdraw")
}
func TestRejectedOperationsAtomic(t *testing.T) {
	if _, e := New(0); !errors.Is(e, ErrInvalidMaxLeft) {
		t.Fatalf("New(0): %v", e)
	}
	es := map[error]struct{}{ErrInvalidMaxLeft: {}, ErrLeftIDExists: {}, ErrLeftTableFull: {}, ErrLeftNotFound: {}, ErrRightUnderflow: {}}
	if len(es) != 5 {
		t.Fatal("sentinels must be distinct")
	}
	s, _ := New(1)
	must(t, s.AddLeft(1, strptr("a")))
	cs := []stp{{'L', 1, strptr("b"), nil}, {'L', 2, strptr("a"), nil}, {'l', 9, nil, nil}, {'r', 0, strptr("z"), nil}}
	ws := []error{ErrLeftIDExists, ErrLeftTableFull, ErrLeftNotFound, ErrRightUnderflow}
	for i, c := range cs {
		before := s.View()
		if e := apply(s, c); !errors.Is(e, ws[i]) {
			t.Fatalf("%v: %v", ws[i], e)
		}
		ck(t, s.View(), before, "rejected op trace")
	}
	must(t, s.AddRight(strptr("b")))
	ck(t, s.View(), []int64{}, "rejected dup key index")
	must(t, s.AddRight(strptr("a")))
	ck(t, s.View(), []int64{1}, "usable after rejects")
}
func TestRightLookupCounterIndexed(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(m + 1)
		for i := 0; i < m; i++ {
			v := "k" + strconv.Itoa(i)
			must(t, s.AddLeft(int64(i+1), &v))
		}
		must(t, s.AddRight(strptr("k0")))
		add := s.lastScan
		must(t, s.DelRight(strptr("k0")))
		if add != 1 || s.lastScan != 1 {
			t.Fatalf("m=%d scans add=%d del=%d, want 1,1", m, add, s.lastScan)
		}
	}
}
func TestConcurrentReadersAgree(t *testing.T) {
	s, _ := New(256)
	for i := 1; i <= 128; i++ {
		v := "k" + strconv.Itoa(i)
		must(t, s.AddLeft(int64(i), &v))
		must(t, s.AddRight(&v))
	}
	base, start, done := s.View(), make(chan struct{}), make(chan error, 16)
	for g := 0; g < 16; g++ {
		go func() {
			<-start
			for r := 0; r < 500; r++ {
				if !reflect.DeepEqual(s.View(), base) {
					done <- errors.New("views differ")
					return
				}
			}
			done <- nil
		}()
	}
	close(start)
	for g := 0; g < 16; g++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
}
func TestKeyMatch(t *testing.T) {
	a, b, e := strptr("a"), strptr("b"), strptr("")
	xs := []*string{nil, nil, a, a, a, e, e}
	ys := []*string{nil, a, nil, a, b, nil, e}
	ws := []bool{false, false, false, true, false, false, true}
	for i := range ws {
		if key.Match(xs[i], ys[i]) != ws[i] {
			t.Fatalf("case %d", i)
		}
	}
}
