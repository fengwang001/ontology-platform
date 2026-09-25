package api_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// naiveRef 朴素参照：rate 逐个网格点步进跳过，delay 按结束+interval。
func naiveRef(mode api.Mode, interval int64, durs []int64) [][2]int64 {
	out := make([][2]int64, 0, len(durs))
	var lastEnd int64
	for i, d := range durs {
		start := lastEnd + interval
		if mode == api.Rate {
			for start = 0; start < lastEnd; start += interval {
			}
		} else if i == 0 {
			start = 0
		}
		out = append(out, [2]int64{start, start + d})
		lastEnd = start + d
	}
	return out
}

func randDurs(seed, n int64) []int64 {
	d := make([]int64, n)
	for i := range d {
		d[i] = (int64(i)*37 + seed*11) % 53 // 含 0 与跨多网格点的耗时
	}
	return d
}

func runSeq(t *testing.T, mode api.Mode, interval int64, durs []int64) [][2]int64 {
	s := api.New()
	if err := s.AddTask("t", mode, interval); err != nil {
		t.Fatal(err)
	}
	got := make([][2]int64, 0, len(durs))
	for _, d := range durs {
		st, en, err := s.Run("t", d)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]int64{st, en})
	}
	return got
}

// checkInv 校验单任务序列的不变量 2（rate 不漂移）/3（delay 漂移）。
func checkInv(t *testing.T, mode api.Mode, iv int64, seq [][2]int64) {
	for i, se := range seq {
		if mode == api.Rate && se[0]%iv != 0 {
			t.Fatalf("rate 漂移: start=%d", se[0])
		}
		if mode == api.Delay && i > 0 && se[0] != seq[i-1][1]+iv {
			t.Fatalf("delay 漂移不一致: start=%d", se[0])
		}
	}
}

func TestMatchesNaiveReference(t *testing.T) {
	for _, mode := range []api.Mode{api.Rate, api.Delay} {
		for _, iv := range []int64{1, 3, 10, 97} {
			for seed := int64(0); seed < 5; seed++ {
				durs := randDurs(seed, 40)
				got, want := runSeq(t, mode, iv, durs), naiveRef(mode, iv, durs)
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("mode=%d iv=%d i=%d got=%v want=%v", mode, iv, i, got[i], want[i])
					}
				}
			}
		}
	}
}

func TestRateNoDrift(t *testing.T) {
	for _, iv := range []int64{1, 7, 10, 1000} {
		checkInv(t, api.Rate, iv, runSeq(t, api.Rate, iv, randDurs(3, 50)))
	}
}

func TestDelayDrift(t *testing.T) {
	for _, iv := range []int64{1, 7, 10, 1000} {
		checkInv(t, api.Delay, iv, runSeq(t, api.Delay, iv, randDurs(4, 50)))
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	s := api.New()
	if err := s.AddTask("a", api.Rate, 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Run("a", 3); err != nil {
		t.Fatal(err)
	}
	_, _, durErr := s.Run("a", -1)
	bads := []error{durErr, s.AddTask("", api.Rate, 1), s.AddTask("a", api.Rate, 1), s.AddTask("b", api.Mode(42), 1), s.AddTask("c", api.Delay, 0)}
	sents := []error{api.ErrBadDuration, api.ErrEmptyID, api.ErrDuplicateID, api.ErrUnknownMode, api.ErrBadInterval}
	for i := range bads { // 哨兵互不相同，逐一相等即同时证明可判定与可区分
		if bads[i] != sents[i] {
			t.Fatalf("第 %d 个坏操作: got %v want %v", i, bads[i], sents[i])
		}
	}
	// 状态不变：任务 a 下次开始仍为 10，且调度器仍可正常使用。
	if st, en, err := s.Run("a", 2); err != nil || st != 10 || en != 12 {
		t.Fatalf("拒绝后状态被改变: start=%d end=%d err=%v", st, en, err)
	}
	if err := s.AddTask("b", api.Delay, 5); err != nil {
		t.Fatalf("拒绝后无法继续注册: %v", err)
	}
}

func TestConcurrentRun(t *testing.T) {
	s := api.New()
	seqs := make([][][2]int64, 16)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		id := fmt.Sprintf("t%d", g)
		if err := s.AddTask(id, api.Mode(g%2), 10); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(g int, id string) {
			defer wg.Done()
			for _, d := range randDurs(int64(g), 60) {
				st, en, err := s.Run(id, d)
				if err != nil {
					t.Error(err)
					return
				}
				seqs[g] = append(seqs[g], [2]int64{st, en})
			}
		}(g, id)
	}
	wg.Wait()
	for g := 0; g < 16; g++ {
		checkInv(t, api.Mode(g%2), 10, seqs[g])
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
