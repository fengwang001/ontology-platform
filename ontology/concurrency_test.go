package ontology

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 并发查询与并发变更（优先级调整、链接增删）混合执行：
// 每次查询的结果必须等价于把所有操作排成某个串行顺序后的结果，
// 即结果必须精确对应某一确定时点，不得出现跨时点的拼接。
func TestConcurrentQueriesAndMutations(t *testing.T) {
	s := newFixture(t).
		obj("A", "B", "C", "D").
		ltype("T1", 1, rp("r", 1)).
		ltype("T2", 2, rp("r", 2)).
		ltype("T3", 10, rp("r2", 1)).
		ltype("T4", 20, rp("r2", 1)).
		link("T1", "A", "B").
		link("T2", "A", "C").
		link("T3", "B", "D").
		link("T4", "C", "D").
		store()

	// 任意串行时点上只有两种合法路径及其代价。
	validPaths := map[[2]LinkTypeName]int64{
		{"T1", "T3"}: 11,
		{"T2", "T4"}: 22,
	}
	validObjs := map[[2]LinkTypeName][]ObjectID{
		{"T1", "T3"}: {"A", "B", "D"},
		{"T2", "T4"}: {"A", "C", "D"},
	}

	check := func(res Result) error {
		switch res.Status {
		case StatusOK:
			if len(res.Steps) != 2 {
				return fmt.Errorf("OK with %d steps", len(res.Steps))
			}
			key := [2]LinkTypeName{res.Steps[0].LinkType, res.Steps[1].LinkType}
			wantCost, ok := validPaths[key]
			if !ok {
				return fmt.Errorf("torn path %v", key)
			}
			if res.TotalCost != wantCost {
				return fmt.Errorf("path %v cost = %d, want %d", key, res.TotalCost, wantCost)
			}
			if got := stepObjs(res); !slices.Equal(got, validObjs[key]) {
				return fmt.Errorf("path %v objs = %v", key, got)
			}
			for i, st := range res.Steps {
				if st.Index != i || st.Role != twoBranchQuery.Roles[i] {
					return fmt.Errorf("malformed step %+v", st)
				}
			}
			return nil
		case StatusAmbiguous:
			// 唯一可能的歧义：T1、T2 在角色 r 上并列优先级 1。
			if len(res.Ambiguous) != 1 {
				return fmt.Errorf("ambiguous steps = %+v", res.Ambiguous)
			}
			a := res.Ambiguous[0]
			if a.Index != 0 || a.At != "A" || a.Role != "r" || a.Priority != 1 {
				return fmt.Errorf("ambiguous step = %+v", a)
			}
			if !slices.Equal(a.Candidates, []LinkTypeName{"T1", "T2"}) {
				return fmt.Errorf("candidates = %v", a.Candidates)
			}
			return nil
		default:
			return fmt.Errorf("unexpected status %v", res.Status)
		}
	}

	var stop atomic.Bool
	var wg sync.WaitGroup

	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				switch (id + i) % 5 {
				case 0:
					_ = s.SetRolePriority("T2", "r", 0)
				case 1:
					_ = s.SetRolePriority("T2", "r", 2)
				case 2:
					_ = s.SetRolePriority("T2", "r", 1) // 与 T1 并列，产生歧义
				case 3:
					s.RemoveLink("T1", "A", "B")
				default:
					if err := s.AddLink("T1", "A", "B"); err != nil {
						t.Errorf("AddLink: %v", err)
					}
				}
			}
		}(w)
	}

	var queries atomic.Int64
	var failures atomic.Int64
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				res := s.Query(twoBranchQuery)
				queries.Add(1)
				if err := check(res); err != nil {
					failures.Add(1)
					t.Errorf("non-serializable result: %v (%s)", err, formatResult(res))
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	t.Logf("checked %d concurrent queries, %d failures", queries.Load(), failures.Load())
	if queries.Load() == 0 {
		t.Fatal("no queries executed")
	}
}

// 纯并发只读查询：所有协程必须得到完全一致的结果。
func TestConcurrentReadersSameResult(t *testing.T) {
	s := twoBranch(t, 1, 2)
	want := s.Query(twoBranchQuery)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got := s.Query(twoBranchQuery)
				if got.Status != want.Status ||
					got.TotalCost != want.TotalCost ||
					!slices.Equal(got.Steps, want.Steps) {
					t.Errorf("got %s, want %s", formatResult(got), formatResult(want))
					return
				}
			}
		}()
	}
	wg.Wait()
}
