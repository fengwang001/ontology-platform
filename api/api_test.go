package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type tcase struct {
	name    string
	quantum int64
	ps      [][3]int64 // {pid, arrival, burst}
}

type badAdd struct {
	pid, a, burst int64
	want          error
}

func table() []tcase { // 固定用例 + 循环生成的随机用例
	tab := []tcase{
		{"notes-q4", 4, [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}}},
		{"q1-simultaneous", 1, [][3]int64{{1, 0, 3}, {2, 0, 1}, {3, 0, 2}}},
		{"big-quantum", 100, [][3]int64{{1, 0, 5}, {2, 0, 9}, {3, 0, 1}}},
		{"idle-gap", 3, [][3]int64{{5, 2, 7}, {2, 0, 1}, {9, 9, 4}, {4, 3, 3}}},
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 30; i++ {
		ps := make([][3]int64, 1+rng.Intn(12))
		for j := range ps {
			ps[j] = [3]int64{int64(j + 1), rng.Int63n(50), 1 + rng.Int63n(30)}
		}
		tab = append(tab, tcase{"rand", 1 + rng.Int63n(10), ps})
	}
	return tab
}

func runCase(t *testing.T, c tcase) map[int64]int64 {
	t.Helper()
	s, err := New(c.quantum)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range c.ps {
		if err := s.Add(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Run()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// 不变量 1：事件驱动的 Run 与逐 tick 朴素参照结果一致。
func TestMatchesNaiveReference(t *testing.T) {
	for _, c := range table() {
		got, want := runCase(t, c), naiveSim(c.quantum, c.ps)
		if len(got) != len(want) {
			t.Fatalf("%s: got %d completions, want %d", c.name, len(got), len(want))
		}
		for pid, ct := range want {
			if got[pid] != ct {
				t.Errorf("%s q=%d pid=%d: got %d want %d", c.name, c.quantum, pid, got[pid], ct)
			}
		}
	}
}

// 不变量 3：完成时刻 >= arrival+burst 且互不相同。
func TestCompletionTimesValid(t *testing.T) {
	for _, c := range table() {
		got, seen := runCase(t, c), make(map[int64]int64)
		for _, p := range c.ps {
			ct, ok := got[p[0]]
			if !ok {
				t.Fatalf("%s: pid=%d missing", c.name, p[0])
			}
			if ct < p[1]+p[2] {
				t.Errorf("%s pid=%d: completion=%d < %d", c.name, p[0], ct, p[1]+p[2])
			}
			if prev, dup := seen[ct]; dup {
				t.Errorf("%s: pids %d,%d share completion %d", c.name, prev, p[0], ct)
			}
			seen[ct] = p[0]
		}
	}
}

// 不变量 4：三类拒绝可判定、互不相同、不留痕，之后仍可正常使用。
func TestRejectedOpsKeepState(t *testing.T) {
	for _, q := range []int64{0, -3} {
		_, err := New(q)
		if !errors.Is(err, ErrInvalidQuantum) || errors.Is(err, ErrInvalidProcess) || errors.Is(err, ErrDuplicatePID) {
			t.Fatalf("quantum=%d: err=%v not distinguishable", q, err)
		}
	}
	s, _ := New(2)
	for _, p := range [][3]int64{{1, 0, 3}, {2, 1, 1}} {
		if err := s.Add(p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.Run()
	bads := []badAdd{
		{3, -1, 1, ErrInvalidProcess}, {4, 0, 0, ErrInvalidProcess}, {5, 0, -2, ErrInvalidProcess},
		{1, 0, 1, ErrDuplicatePID}, {2, 3, 3, ErrDuplicatePID},
	}
	other := map[error]error{ErrInvalidProcess: ErrDuplicatePID, ErrDuplicatePID: ErrInvalidProcess}
	for _, b := range bads {
		if err := s.Add(b.pid, b.a, b.burst); !errors.Is(err, b.want) || errors.Is(err, other[b.want]) {
			t.Errorf("add %+v: err=%v, want %v (distinguishable)", b, err, b.want)
		}
	}
	after, _ := s.Run()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed: %v -> %v", before, after)
	}
	if err := s.Add(9, 0, 1); err != nil {
		t.Errorf("add after rejections: %v", err)
	}
}

// SelfCheck 可并发调用且全部通过。
func TestSelfCheckConcurrent(t *testing.T) {
	s, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
