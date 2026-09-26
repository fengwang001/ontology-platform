package rr

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/proc"
)

// naiveTick：测试侧独立实现的逐 tick 朴素参照，队头每 tick 减 1。
func naiveTick(ps []proc.Proc, qn int64) map[int64]int64 {
	in := ordered(ps)
	rem, done := map[int64]int64{}, map[int64]int64{}
	for _, p := range in {
		rem[p.PID] = p.Burst
	}
	var q []int64
	idx, t, ran := 0, int64(0), int64(0)
	for idx < len(in) || len(q) > 0 {
		for idx < len(in) && in[idx].Arrival <= t {
			q, idx = append(q, in[idx].PID), idx+1
		}
		if len(q) == 0 {
			t, ran = in[idx].Arrival, 0
			continue
		}
		cur := q[0]
		t, rem[cur], ran = t+1, rem[cur]-1, ran+1
		for idx < len(in) && in[idx].Arrival <= t {
			q, idx = append(q, in[idx].PID), idx+1
		}
		if rem[cur] == 0 {
			done[cur], q, ran = t, q[1:], 0
		} else if ran == qn {
			q, ran = append(q[1:], cur), 0
		}
	}
	return done
}

func genCases(seed int64, rounds int) [][]proc.Proc {
	rng, out := rand.New(rand.NewSource(seed)), make([][]proc.Proc, 0, rounds)
	for g := 0; g < rounds; g++ {
		n, ps := 1+rng.Intn(10), make([]proc.Proc, 0, 10)
		for i := 0; i < n; i++ {
			p, _ := proc.New(int64(i+1), rng.Int63n(20), int64(1+rng.Intn(15)))
			ps = append(ps, p)
		}
		out = append(out, ps)
	}
	return out
}

func addAll(t *testing.T, s *Scheduler, ps []proc.Proc) {
	t.Helper()
	for _, p := range ps {
		if err := s.Add(p.PID, p.Arrival, p.Burst); err != nil {
			t.Fatal(err)
		}
	}
}

// canonical：第三节四进程 P1(0,10) P2(1,4) P3(3,3) P4(3,2)。
var canonical = []proc.Proc{
	{PID: 1, Arrival: 0, Burst: 10, Remaining: 10},
	{PID: 2, Arrival: 1, Burst: 4, Remaining: 4},
	{PID: 3, Arrival: 3, Burst: 3, Remaining: 3},
	{PID: 4, Arrival: 3, Burst: 2, Remaining: 2},
}

// TestEquivNaive 钉住不变量 1：整块推进与逐 tick 朴素参照逐 pid 一致。
func TestEquivNaive(t *testing.T) {
	cases := append([][]proc.Proc{canonical}, genCases(42, 60)...)
	for _, qn := range []int64{1, 2, 3, 4, 9} {
		for ci, ps := range cases {
			s, _ := New(qn)
			addAll(t, s, ps)
			got, want := s.Run(), naiveTick(ps, qn)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("q=%d case=%d got %v want %v", qn, ci, got, want)
			}
		}
	}
}

// TestConservation 钉住不变量 2：每进程被服务总时长恰等于 burst。
func TestConservation(t *testing.T) {
	for ci, ps := range genCases(7, 40) {
		s, _ := New(int64(1 + ci%5))
		addAll(t, s, ps)
		_, sv := s.simulate()
		for _, p := range ps {
			if sv[p.PID] != p.Burst {
				t.Fatalf("case=%d pid=%d served %d!=burst %d", ci, p.PID, sv[p.PID], p.Burst)
			}
		}
	}
}

// TestCompletionBounds 钉住不变量 3：完成>=arrival+burst 且完成时刻互不相同。
func TestCompletionBounds(t *testing.T) {
	for ci, ps := range genCases(99, 40) {
		s, _ := New(int64(1 + ci%7))
		addAll(t, s, ps)
		got, seen := s.Run(), map[int64]int64{}
		for _, p := range ps {
			c := got[p.PID]
			if c < p.Arrival+p.Burst {
				t.Fatalf("case=%d pid=%d completion %d<%d", ci, p.PID, c, p.Arrival+p.Burst)
			}
			if other, ok := seen[c]; ok {
				t.Fatalf("case=%d pid=%d shares %d with %d", ci, p.PID, c, other)
			}
			seen[c] = p.PID
		}
	}
}

// TestCanonical 钉住第三节四进程 quantum=4 手算结果 P1=19 P2=8 P3=11 P4=13。
func TestCanonical(t *testing.T) {
	s, _ := New(4)
	addAll(t, s, canonical)
	want := map[int64]int64{1: 19, 2: 8, 3: 11, 4: 13}
	if got := s.Run(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestTickStepsNotLinearInM 钉住整块推进：非导出计数器不随 m 线性增长，恒为 0。
func TestTickStepsNotLinearInM(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		s, _ := New(m) // quantum=m >= burst=m
		if err := s.Add(1, 0, m); err != nil {
			t.Fatal(err)
		}
		if c := s.Run(); c[1] != m || s.tickSteps != 0 { // 白盒直读非导出字段
			t.Fatalf("m=%d: whole-slice advance violated (completion=%d ticks=%d)", m, c[1], s.tickSteps)
		}
	}
}

func TestSelfTest(t *testing.T) {
	if err := SelfTest(); err != nil {
		t.Fatal(err)
	} else if err := SelfTestCounter(); err != nil {
		t.Fatal(err)
	}
}
