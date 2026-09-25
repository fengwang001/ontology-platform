package stream_test

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/bound"
	"ontology/sketch"
	"ontology/stream"
)

func newStream(t *testing.T, w, d int, max uint64) *stream.Stream {
	t.Helper()
	s, err := stream.New(stream.Config{Width: w, Depth: d, Phi: 0.1, MaxTotal: max})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEndToEndNeverUnderestimate(t *testing.T) {
	s := newStream(t, 64, 4, 0)
	truth := map[string]uint64{}
	for i := 0; i < 500; i++ {
		k := "key" + strconv.Itoa(i%53)
		if err := s.Add(k, 1); err != nil {
			t.Fatal(err)
		}
		truth[k]++
	}
	for k, c := range truth {
		if est, _ := s.Estimate(k); est < c {
			t.Fatalf("Estimate(%q)=%d < true %d", k, est, c)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityRejectAndRecover(t *testing.T) {
	s := newStream(t, 16, 3, 10)
	s.Add("a", 6)
	before := s.Cells()
	if err := s.Add("b", 5); !errors.Is(err, stream.ErrCapacity) {
		t.Fatalf("err=%v, want ErrCapacity", err)
	}
	if !reflect.DeepEqual(s.Cells(), before) {
		t.Fatal("rejected Add changed cells")
	}
	if err := s.Add("b", 4); err != nil { // still usable, not a terminal state
		t.Fatalf("stream unusable after rejection: %v", err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestDistinctDecidableErrors(t *testing.T) {
	s := newStream(t, 16, 3, 1)
	s.Add("a", 1)
	o := newStream(t, 32, 3, 0)
	_, errDims := stream.New(stream.Config{Width: 0, Depth: 3, Phi: .1})
	_, errProb := stream.New(stream.Config{Epsilon: 1.5, Delta: .5, Phi: .1})
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"bad dimensions", errDims, sketch.ErrBadDimensions},
		{"bad probability", errProb, bound.ErrBadProbability},
		{"empty key", s.Add("", 1), sketch.ErrEmptyKey},
		{"capacity", s.Add("b", 1), stream.ErrCapacity},
		{"param mismatch", s.Merge(o), sketch.ErrIncompatible},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: err=%v, want %v", c.name, c.err, c.want)
		}
		if seen[c.err] {
			t.Fatalf("%s: error %v not distinct", c.name, c.err)
		}
		seen[c.err] = true
	}
}

func TestMergeCrossParamsLeavesBothIntact(t *testing.T) {
	a := newStream(t, 16, 3, 0)
	b := newStream(t, 16, 4, 0)
	a.Add("x", 3)
	b.Add("y", 5)
	ca, cb := a.Cells(), b.Cells()
	if err := a.Merge(b); !errors.Is(err, sketch.ErrIncompatible) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(a.Cells(), ca) || !reflect.DeepEqual(b.Cells(), cb) {
		t.Fatal("rejected merge changed a stream")
	}
}

func TestConcurrentQueriesIdentical(t *testing.T) {
	s := newStream(t, 64, 4, 0)
	for i := 0; i < 200; i++ {
		s.Add("k"+strconv.Itoa(i%23), 1)
	}
	type result struct {
		est uint64
		hh  []string
		err error
	}
	want := result{}
	want.est, _ = s.Estimate("k0")
	want.hh = s.HeavyHitters()
	want.err = s.SelfCheck()
	const n = 16
	got := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			got[g].est, _ = s.Estimate("k0")
			got[g].hh = s.HeavyHitters()
			got[g].err = s.SelfCheck()
		}(g)
	}
	close(start)
	wg.Wait()
	for g, r := range got {
		if r.est != want.est || !reflect.DeepEqual(r.hh, want.hh) || r.err != want.err {
			t.Fatalf("goroutine %d got %+v, want %+v", g, r, want)
		}
	}
}
