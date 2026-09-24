package audit_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/audit"
	"ontology/handle"
	"ontology/table"
)

func TestCheckAndStats(t *testing.T) {
	tab, _ := table.New(8)
	var hs []handle.Handle
	for i := 0; i < 5; i++ {
		h, _ := tab.Insert(i)
		hs = append(hs, h)
	}
	tab.Remove(hs[1])
	tab.Remove(hs[3])
	if err := audit.Check(tab); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	st := audit.Collect(tab)
	if st.Live != 3 || st.Free != 5 || st.Exhausted != 0 || st.Cap != 8 {
		t.Errorf("Stats = %+v, want Live=3 Free=5 Exhausted=0 Cap=8", st)
	}
	if len(st.Generations) != 8 || st.Generations[1] != 2 || st.Generations[0] != 1 {
		t.Errorf("Generations = %v", st.Generations)
	}
	if st.Live != tab.Len() {
		t.Errorf("Live %d != Len %d", st.Live, tab.Len())
	}
}

// ABA 专项：一个 goroutine 反复 Remove+Insert 迫使槽位复用，
// 8 个 goroutine 持已失效老句柄反复 Get，一次都不得成功。
func TestABAStaleHandleNeverSucceeds(t *testing.T) {
	tab, _ := table.New(1)
	victim, _ := tab.Insert(0)
	tab.Remove(victim)
	var success atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := tab.Get(victim); err == nil {
					success.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		h, err := tab.Insert(i)
		if err != nil {
			t.Fatalf("Insert err = %v", err)
		}
		if err := tab.Remove(h); err != nil {
			t.Fatalf("Remove err = %v", err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if got := success.Load(); got != 0 {
		t.Fatalf("stale handle succeeded %d times, want 0", got)
	}
	if err := audit.Check(tab); err != nil {
		t.Fatalf("Check after ABA = %v, want nil", err)
	}
	live, dead := 0, 0
	for _, s := range tab.Slots() {
		if s.InUse {
			live++
		}
		if s.Exhausted {
			dead++
		}
	}
	if live+len(tab.FreeIndices())+dead != tab.Cap() {
		t.Fatal("Len + free + exhausted != Cap after ABA")
	}
}
