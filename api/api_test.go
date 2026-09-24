package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/store"
)

func render(rs []store.Range) (out string) {
	for _, r := range rs {
		out += fmt.Sprintf("[%d,%d)%d%v;", r.Lo, r.Hi, r.Load, r.Keys)
	}
	return
}

func TestElevenSteps(t *testing.T) {
	s, _ := api.New(0, 16, 3, 2)
	ops := []func(){
		func() { s.Insert(5) }, func() { s.Insert(11) }, func() { s.Insert(8) },
		func() { s.Insert(14) }, func() { s.Insert(2) }, func() { s.Insert(6) },
		func() { s.Insert(12) }, func() { s.Insert(10) }, func() { s.Delete(5) }, func() { s.Delete(2) }, func() { s.Compact() },
	}
	want := []string{
		"[0,16)1[5];", "[0,16)2[5 11];",
		"[0,8)1[5];[8,16)2[8 11];", "[0,8)1[5];[8,12)2[8 11];[12,16)1[14];",
		"[0,8)2[2 5];[8,12)2[8 11];[12,16)1[14];", "[0,4)1[2];[4,8)2[5 6];[8,12)2[8 11];[12,16)1[14];",
		"[0,4)1[2];[4,8)2[5 6];[8,12)2[8 11];[12,16)2[12 14];", "[0,4)1[2];[4,8)2[5 6];[8,10)1[8];[10,12)2[10 11];[12,16)2[12 14];",
		"[0,4)1[2];[4,8)1[6];[8,10)1[8];[10,12)2[10 11];[12,16)2[12 14];", "[0,4)0[];[4,8)1[6];[8,10)1[8];[10,12)2[10 11];[12,16)2[12 14];",
		"[0,10)2[6 8];[10,12)2[10 11];[12,16)2[12 14];",
	}
	for i, op := range ops {
		op()
		if got := render(s.Ranges()); got != want[i] {
			t.Errorf("step %d: got %s want %s", i+1, got, want[i])
		}
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	for _, tc := range []struct{ split, merge, space, ops int }{
		{3, 2, 16, 500}, {4, 2, 4096, 2000}, {2, 1, 1000, 2000}, {9, 3, 777, 1500},
	} {
		for seed := int64(0); seed < 3; seed++ {
			s, _ := api.New(0, int64(tc.space), tc.split, tc.merge)
			model := map[int64]bool{}
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < tc.ops; i++ {
				key := rng.Int63n(int64(tc.space))
				switch rng.Intn(3) {
				case 0:
					if err := s.Insert(key); err != nil {
						t.Fatalf("%+v seed %d: %v", tc, seed, err)
					}
					model[key] = true
				case 1:
					err := s.Delete(key)
					if ok := model[key]; ok != (err == nil) || (!ok && !errors.Is(err, api.ErrKeyNotFound)) {
						t.Fatalf("%+v seed %d key %d: %v", tc, seed, key, err)
					}
					delete(model, key)
				case 2:
					s.Compact()
				}
			}
			if err := s.Verify(model); err != nil {
				t.Fatalf("%+v seed %d: %v", tc, seed, err)
			}
			if lo, hi := s.Bounds(); lo != 0 || hi != int64(tc.space) {
				t.Fatalf("%+v seed %d: coverage [%d,%d)", tc, seed, lo, hi)
			}
		}
	}
}

func TestErrorsDistinctAndStateless(t *testing.T) {
	bad := [][4]int64{{5, 5, 3, 2}, {9, 2, 3, 2}, {-1, 8, 3, 2}, {0, 8, 1, 2},
		{0, 8, 3, 0}, {0, 8, 2, 2}, {0, 8, 2, 5}}
	for _, b := range bad {
		if _, err := api.New(b[0], b[1], int(b[2]), int(b[3])); !errors.Is(err, api.ErrBadParam) {
			t.Errorf("%v: want ErrBadParam, got %v", b, err)
		}
	}
	for _, p := range [][2]error{{api.ErrBadParam, api.ErrKeyOutOfRange},
		{api.ErrBadParam, api.ErrKeyNotFound}, {api.ErrKeyOutOfRange, api.ErrKeyNotFound}} {
		if errors.Is(p[0], p[1]) {
			t.Errorf("errors not distinct: %v vs %v", p[0], p[1])
		}
	}
	s, _ := api.New(0, 16, 3, 2)
	s.Insert(5)
	before := s.Ranges()
	if err := s.Insert(16); !errors.Is(err, api.ErrKeyOutOfRange) {
		t.Errorf("insert out of range: %v", err)
	}
	if _, err := s.Locate(-1); !errors.Is(err, api.ErrKeyOutOfRange) {
		t.Errorf("locate out of range: %v", err)
	}
	if err := s.Delete(7); !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("delete absent: %v", err)
	}
	if !reflect.DeepEqual(before, s.Ranges()) {
		t.Error("rejected operations changed state")
	}
	if err := s.Insert(6); err != nil {
		t.Errorf("set unusable after rejections: %v", err)
	}
}

func TestConcurrentLocate(t *testing.T) {
	s, _ := api.New(0, 4096, 2, 1)
	var keys []int64
	for k := int64(0); len(s.Ranges()) < 500; k++ {
		s.Insert(k)
		keys = append(keys, k)
	}
	got := make([][]store.Range, 32)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := range got {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for _, k := range keys {
				r, _ := s.Locate(k)
				got[g] = append(got[g], r)
			}
			_ = s.Ranges()
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < len(got); g++ {
		if !reflect.DeepEqual(got[0], got[g]) {
			t.Fatalf("goroutine %d disagrees", g)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
