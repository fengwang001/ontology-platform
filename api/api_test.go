package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func naive(r, s []Key) map[Pair]int {
	c := map[Pair]int{}
	for _, a := range r {
		for _, b := range s {
			if a == b {
				c[Pair{RKey: a, SKey: b}]++
			}
		}
	}
	return c
}
func canon(ps []Pair) map[Pair]int {
	c := map[Pair]int{}
	for _, p := range ps {
		c[p]++
	}
	return c
}

func TestJoinMatchesNaive(t *testing.T) {
	type tc struct {
		m, fanIn int
		r, s     []Key
	}
	cases := []tc{
		{3, 2, []Key{5, 1, 3, 7, 3, 2}, []Key{3, 6, 3, 2}},
		{1, 3, []Key{2, 2, 1}, []Key{2, 1, 1}},
		{5, 2, []Key{9, 0, 3}, []Key{3, 3, 9}},
	}
	for seed := int64(0); seed < 60; seed++ { // 多档 M + 随机输入循环生成
		rnd := rand.New(rand.NewSource(seed))
		mv := []int{1, 2, 3, 5}[rnd.Intn(4)]
		n := rnd.Intn(60)
		r, s := make([]Key, n), make([]Key, n)
		for i := range r {
			r[i], s[i] = Key(rnd.Intn(8)), Key(rnd.Intn(8))
		}
		cases = append(cases, tc{mv, 100, r, s})
	}
	for i, c := range cases {
		e, err := New(c.m, c.fanIn)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.BuildR(c.r); err != nil {
			t.Fatal(err)
		}
		if err := e.BuildS(c.s); err != nil {
			t.Fatal(err)
		}
		got, err := e.Join()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(canon(got), naive(c.r, c.s)) {
			t.Fatalf("case %d multiset mismatch: %v", i, got)
		}
		if err := e.SelfCheck(); err != nil {
			t.Fatalf("case %d selfcheck: %v", i, err)
		}
	}
}

func TestRejectionLeavesState(t *testing.T) {
	if _, err := New(0, 2); !errors.Is(err, ErrBadThreshold) {
		t.Fatalf("M<1: %v", err)
	}
	if _, err := New(3, 1); !errors.Is(err, ErrBadFanIn) {
		t.Fatalf("fanIn<2: %v", err)
	}
	if ErrBadThreshold == ErrBadFanIn || ErrBadFanIn == ErrNegativeKey || ErrBadThreshold == ErrNegativeKey {
		t.Fatal("sentinel errors must be distinct")
	}
	e, _ := New(3, 100)
	if err := e.BuildR([]Key{5, 1, 3, 7, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if err := e.BuildS([]Key{3, 6, 3, 2}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.Join()
	if err := e.BuildR([]Key{1, -2, 3}); !errors.Is(err, ErrNegativeKey) {
		t.Fatalf("negative: %v", err)
	}
	after, _ := e.Join()
	if !reflect.DeepEqual(canon(after), canon(before)) {
		t.Fatal("rejected batch changed state")
	}
	if err := e.BuildS([]Key{-1}); !errors.Is(err, ErrNegativeKey) {
		t.Fatalf("negative S: %v", err)
	}
	if err := e.BuildR([]Key{1, 2}); err != nil { // 拒绝后仍可正常使用
		t.Fatalf("engine unusable after rejection: %v", err)
	}
}

func TestFanInRejected(t *testing.T) {
	e, _ := New(1, 2)
	_ = e.BuildR([]Key{1, 2, 3}) // 3 个 run > fanIn 2
	_ = e.BuildS([]Key{1})
	if _, err := e.Join(); !errors.Is(err, ErrFanInExceeded) {
		t.Fatalf("fan-in exceeded: %v", err)
	}
}

func TestConcurrentJoin(t *testing.T) {
	e, _ := New(2, 100)
	rnd := rand.New(rand.NewSource(7))
	r, s := make([]Key, 200), make([]Key, 200)
	for i := range r {
		r[i], s[i] = Key(rnd.Intn(10)), Key(rnd.Intn(10))
	}
	if err := e.BuildR(r); err != nil {
		t.Fatal(err)
	}
	if err := e.BuildS(s); err != nil {
		t.Fatal(err)
	}
	const n = 32
	var wg sync.WaitGroup
	res := make([]map[Pair]int, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ps, err := e.Join()
			if err != nil {
				t.Errorf("join: %v", err)
				return
			}
			res[g] = canon(ps)
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if !reflect.DeepEqual(res[g], res[0]) {
			t.Fatalf("goroutine %d got different multiset", g)
		}
	}
}
