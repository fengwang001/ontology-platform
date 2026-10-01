package regions

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentReplaceAtomic 高并发下持续 Replace 同一 id，
// Locate 看到的必须是完整一致的某一版区域（旧版或新版），
// 不能出现两者都在、两者都不在或一分为二的瞬间。
func TestConcurrentReplaceAtomic(t *testing.T) {
	s := NewSet()
	old := Region{ID: "z", Priority: 1, Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}}}
	newR := Region{ID: "z", Priority: 1, Outer: Ring{{10, 0}, {18, 0}, {18, 8}, {10, 8}}}
	if err := s.Put(old); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		v := newR
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.Replace(v); err != nil {
				t.Errorf("Replace: %v",
					err)
				return
			}
			if v.Outer[0].X == 0 {
				v = newR
			} else {
				v = old
			}
		}
	}()

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				select {
				case <-stop:
					return
				default:
				}
				inOld, err := s.Locate(4, 4)
				if err != nil {
					t.Errorf("Locate old: %v", err)
					return
				}
				inNew, err := s.Locate(14, 4)
				if err != nil {
					t.Errorf("Locate new: %v", err)
					return
				}
				oldHit := len(inOld) == 1
				newHit := len(inNew) == 1
				// 同一份逻辑区域在任何一时刻只能完整地处于一个位置。
				if oldHit == newHit {
					t.Errorf("non-atomic snapshot: oldHit=%v newHit=%v", oldHit, newHit)
					return
				}
			}
		}()
	}

	// 并发只读 Best 也不得出错。
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if _, _, err := s.Best(4, 4); err != nil {
					t.Errorf("Best: %v", err)
					return
				}
			}
		}()
	}

	close(stop)
	wg.Wait()
	t.Logf("输入: 并发 Replace + Locate/Best -> 输出: 所有快照均为完整一致的旧版或新版；判定依据: 读写锁下 map 单次赋值原子替换")
}

// TestDeterministicReplay 相同操作序列重放两次，所有定位结果完全一致。
func TestDeterministicReplay(t *testing.T) {
	play := func() [][]string {
		s := NewSet()
		ops := []func() error{
			func() error {
				return s.Put(Region{ID: "a", Priority: 1, Outer: Ring{{0, 0}, {8, 0}, {8, 8}, {0, 8}}})
			},
			func() error {
				return s.Put(Region{ID: "b", Priority: 1, Outer: Ring{{2, 2}, {10, 2}, {10, 10}, {2, 10}}})
			},
			func() error {
				return s.Put(Region{ID: "c", Priority: 3,
					Outer: Ring{{0, 0}, {6, 0}, {6, 6}, {0, 6}},
					Hole:  Ring{{2, 2}, {4, 2}, {4, 4}, {2, 4}}})
			},
			func() error {
				return s.Replace(Region{ID: "a", Priority: 5, Outer: Ring{{0, 0}, {5, 0}, {5, 5}, {0, 5}}})
			},
			func() error { return s.Remove("b") },
		}
		for _, op := range ops {
			if err := op(); err != nil {
				t.Fatalf("unexpected op error: %v", err)
			}
		}
		var out [][]string
		for x := 0; x <= 10; x++ {
			for y := 0; y <= 10; y++ {
				got, err := s.Locate(int64(x), int64(y))
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, ids(got))
			}
		}
		return out
	}

	r1 := play()
	r2 := play()
	if fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatalf("replay differs:\n%v\n%v", r1, r2)
	}
	t.Logf("输入: 同一 Put/Put/Put/Replace/Remove 序列重放两次 -> 输出: %d 个查询点结果完全相同", len(r1))
}
