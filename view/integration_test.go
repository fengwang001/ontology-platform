package view

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/agg"
)

func TestDeterminismAcrossBatchSplits(t *testing.T) {
	build := func(splits [][2]int) *agg.Store {
		s := newStore(t)
		var all []agg.Event
		for i := 0; i < 50; i++ {
			all = append(all, ev(int64((i*37)%50), fmt.Sprintf("r%d", i%4), fmt.Sprintf("d%d", i%3)))
		}
		for _, sp := range splits {
			if err := s.Append(all[sp[0]:sp[1]]); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		return s
	}
	s1 := build([][2]int{{0, 50}})
	s2 := build([][2]int{{0, 7}, {7, 33}, {33, 50}})
	t1, err1 := Tabulate(s1, "senior", "region", "dept", 0, 50)
	t2, err2 := Tabulate(s2, "senior", "region", "dept", 0, 50)
	if err1 != nil || err2 != nil {
		t.Fatalf("Tabulate: %v %v", err1, err2)
	}
	if !reflect.DeepEqual(t1, t2) {
		t.Fatalf("批次切分不同导致结果不同:\n%+v\n%+v", t1, t2)
	}
	if t1.Total != 50 {
		t.Fatalf("Total = %d, want 50", t1.Total)
	}
	t.Logf("整批与三批切分得到逐字段相同的 Table, Total=%d", t1.Total)
}

func TestConcurrentAppendAndTabulate(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for b := 0; b < 10; b++ {
				var batch []agg.Event
				for i := 0; i < 10; i++ {
					batch = append(batch, ev(int64(w*100+b*10+i),
						fmt.Sprintf("r%d", w%3), fmt.Sprintf("d%d", b%2)))
				}
				if err := s.Append(batch); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := 0; q < 20; q++ {
				tb, err := Tabulate(s, "senior", "region", "dept", 0, 1000)
				if err != nil {
					t.Errorf("Tabulate: %v", err)
					return
				}
				if tb.Total != -1 { // 未隐藏时总计等于范围内事件数
					n := 0
					for _, row := range tb.Cells {
						for _, c := range row {
							if !c.Suppressed {
								n += c.Count
							}
						}
					}
					if n > tb.Total {
						t.Errorf("可见计数和 %d 超过总计 %d", n, tb.Total)
					}
				}
			}
		}()
	}
	wg.Wait()
	tb, err := Tabulate(s, "senior", "region", "dept", 0, 1000)
	if err != nil {
		t.Fatalf("Tabulate: %v", err)
	}
	if tb.Total != 800 {
		t.Fatalf("并发追加后 Total = %d, want 800", tb.Total)
	}
	t.Logf("8 写 8 读并发后 Total=%d, 等于范围内事件数", tb.Total)
}
