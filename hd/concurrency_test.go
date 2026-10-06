package hd

import (
	"sync"
	"testing"
)

// 大量并发操作下系统不崩溃、无数据竞争；结果必然等价于某个合法串行顺序，
// 最终不变量（无机位同时承担两次治疗）由各次接受操作自身的可行性判定保证。
func TestConcurrentSafety(t *testing.T) {
	s := testSystem(t)
	for i, z := range []Zone{ZoneGeneral, ZoneGeneral, ZoneIsolation} {
		if err := s.RegisterBay(0, bayLetter(i), z, i == 1, true); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		inf := InfectionNegative
		if i == 3 {
			inf = InfectionHBV
		}
		if err := s.RegisterPatient(0, patLetter(i), inf); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				now := w*1000 + i*200
				_, _ = s.BookTreatment(now, cid(w, i), patLetter(w%4), 5000+i*300, 30)
			}
		}(w)
	}
	wg.Wait()

	// 校验：每个机位上任意两次相邻治疗满足消毒/不重叠，且隔离区感染类型一致
	// 或具备深度消毒间隔。这里用朴素全量扫描只做最终断言（不影响生产复杂度）。
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, bayID := range s.bayOrder {
		var list []*Treatment
		for _, tr := range s.treatments {
			if tr.BayID == bayID {
				list = append(list, tr)
			}
		}
		sortByStart(list)
		for i := 1; i < len(list); i++ {
			prev, cur := list[i-1], list[i]
			gap := s.cfg.cleanDuration(prev.Infection)
			if prev.Infection != cur.Infection {
				gap = s.cfg.DeepClean
			}
			if cur.Start < prev.End+gap {
				t.Fatalf("bay %s violates occupancy: %+v vs %+v", bayID, prev, cur)
			}
		}
	}
}

func bayLetter(i int) string { return "B" + itoa(i) }
func patLetter(i int) string { return "P" + itoa(i) }
func cid(w, i int) string    { return "w" + itoa(w) + "-" + itoa(i) }

func sortByStart(list []*Treatment) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1].Start > list[j].Start; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}
