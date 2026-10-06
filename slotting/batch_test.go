package slotting

import (
	"strings"
	"sync"
	"testing"
)

// 批量：失败不留痕；批内重复为参数非法。
func TestBatchAllOrNothing(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 100, 200, 1, false, CatGeneral))
	mustPal(t, s, pallet("ok1", "P", "b", CatGeneral, 10, 10))
	mustPal(t, s, pallet("bad", "P", "b", CatFlammable, 10, 10))
	mustPal(t, s, pallet("ok2", "P", "b", CatGeneral, 10, 10))
	idx, err := s.BatchAutoPlace([]string{"ok1", "bad", "ok2"})
	if idx != 1 || reasonOf(err) != ReasonNoAvailableLocation {
		t.Fatalf("应在下标 1 失败且无可用货位, idx=%d err=%v", idx, err)
	}
	for _, pid := range []string{"ok1", "bad", "ok2"} {
		if _, e := s.PalletLocation(pid); reasonOf(e) != ReasonPalletNotFound {
			t.Fatalf("失败后 %s 不应留痕", pid)
		}
	}
	v, _ := s.GetLocation(LocationID{1, 1, 1})
	if len(v.PalletIDs) != 0 {
		t.Fatalf("失败后货位应为空: %+v", v)
	}
	idx, err = s.BatchAutoPlace([]string{"ok1", "ok1"})
	if idx != 1 || reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("批内重复应参数非法, idx=%d err=%v", idx, err)
	}
	mustLoc(t, s, locOf(LocationID{1, 1, 2}, 100, 200, 1, false, CatGeneral))
	idx, err = s.BatchAutoPlace([]string{"ok1", "ok2"})
	if err != nil || idx != -1 {
		t.Fatalf("整批应成功: idx=%d err=%v", idx, err)
	}
	if id, _ := s.PalletLocation("ok1"); id != (LocationID{1, 1, 1}) {
		t.Fatal(id)
	}
	if id, _ := s.PalletLocation("ok2"); id != (LocationID{1, 1, 2}) {
		t.Fatal(id)
	}
}

func TestLoggerEmits(t *testing.T) {
	w := &sliceWriter{}
	s := NewSystem(w)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 100, 100, 1, true, CatGeneral))
	mustPal(t, s, pallet("p", "P", "b", CatGeneral, 1, 1))
	if _, err := s.AutoPlace("p"); err != nil {
		t.Fatal(err)
	}
	log := string(w.b)
	if !strings.Contains(log, "AutoPlace") || !strings.Contains(log, "成功") {
		t.Fatalf("日志缺少输入/输出: %q", log)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	k := len(b)
	for i > 0 {
		k--
		b[k] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		k--
		b[k] = '-'
	}
	return string(b[k:])
}

// 并发：全部托盘最终在位、不超容量；并发读只见一致快照。
func TestConcurrencyInvariants(t *testing.T) {
	s := NewSystem(nil)
	for a := 1; a <= 4; a++ {
		for i := 1; i <= 6; i++ {
			mustLoc(t, s, locOf(LocationID{a, 1, i}, 1000, 1000, 2, true, CatGeneral))
		}
	}
	const n = 40
	for i := 0; i < n; i++ {
		mustPal(t, s, pallet("p"+itoa(i), "P", "b", CatGeneral, 1, 1))
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.AutoPlace("p" + itoa(i)); err != nil {
				t.Errorf("auto p%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	capUsed := map[LocationID]int{}
	for i := 0; i < n; i++ {
		id, err := s.PalletLocation("p" + itoa(i))
		if err != nil {
			t.Fatalf("p%d 丢失: %v", i, err)
		}
		capUsed[id]++
		if capUsed[id] > 2 {
			t.Fatalf("货位 %s 超容量", id)
		}
	}

	// 移库风暴 + 并发快照读：同一托盘任何时刻恰占一个货位。
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for i := 0; i < n; i++ {
					id, err := s.PalletLocation("p" + itoa(i))
					if err != nil {
						t.Errorf("读到托盘 %d 消失的中间态", i)
						return
					}
					if id == (LocationID{}) {
						t.Errorf("读到空货位中间态")
						return
					}
				}
			}
		}
	}()
	moverDone := make(chan struct{})
	go func() {
		for k := 0; k < 200; k++ {
			i := k % n
			a := (k/6)%4 + 1
			idx := k%6 + 1
			_ = s.Move("p"+itoa(i), LocationID{a, 1, idx}) // 各种拒绝都合法
		}
		close(moverDone)
	}()
	<-moverDone
	close(stop)
	readerWg.Wait()
}
