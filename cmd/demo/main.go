// 布谷鸟哈希演示：退出码 0 表示全部判定通过，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/cuckoo"
)

func fail(format string, a ...any) {
	fmt.Printf("FAIL "+format+"\n", a...)
	os.Exit(1)
}

func slots(s []cuckoo.Slot) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, sl := range s {
		if i > 0 {
			b.WriteByte(' ')
		}
		if sl.Occupied {
			fmt.Fprintf(&b, "%d", sl.Key)
		} else {
			b.WriteByte('-')
		}
	}
	b.WriteByte(']')
	return b.String()
}

func main() {
	// 第三节：n=4 顺序 Insert 0..7，逐步打印 T1/T2。
	h, err := api.New(4, 16)
	if err != nil {
		fail("New: %v", err)
	}
	for x := 0; x < 8; x++ {
		if err := h.Insert(x); err != nil {
			fail("Insert(%d): %v", x, err)
		}
		t1, t2 := h.Snapshot()
		fmt.Printf("OK ins %d: T1=%s T2=%s\n", x, slots(t1), slots(t2))
	}
	// Insert 8 必须 ErrTableFull 且两表不变；Lookup 全部键；四类错误互异；被拒后状态不变。
	b1, b2 := h.Snapshot()
	if err := h.Insert(8); !errors.Is(err, api.ErrTableFull) {
		fail("Insert(8) = %v, want ErrTableFull", err)
	}
	a1, a2 := h.Snapshot()
	for i := range b1 {
		if a1[i] != b1[i] || a2[i] != b2[i] {
			fail("tables changed after ErrTableFull")
		}
	}
	for x := 0; x < 8; x++ {
		if ok, err := h.Lookup(x); !ok || err != nil {
			fail("Lookup(%d) = %v,%v", x, ok, err)
		}
	}
	if _, err := api.New(0, 1); !errors.Is(err, api.ErrInvalidParam) {
		fail("New(0,1) = %v", err)
	}
	if err := h.Insert(3); !errors.Is(err, api.ErrExists) {
		fail("dup Insert = %v", err)
	}
	if _, err := h.Lookup(99); !errors.Is(err, api.ErrNotFound) {
		fail("Lookup miss = %v", err)
	}
	if err := h.Delete(99); !errors.Is(err, api.ErrNotFound) {
		fail("Delete miss = %v", err)
	}
	if h.Len() != 8 {
		fail("Len = %d after rejects, want 8", h.Len())
	}
	fmt.Println("OK Insert(8)=ErrTableFull 且表不变; Lookup 0..7 全真; 四类错误互异; 被拒后状态不变")
	// 大 m 与并发只读：m=10000 下 Lookup 全部与朴素参照一致（槽位数恒 2 由测试钉住），
	// 8 个 goroutine 并发只读结果一致，SelfCheck 通过。
	big, _ := api.New(20000, 128)
	ref := map[int]bool{}
	for i := 0; i < 10000; i++ {
		if err := big.Insert(i * 2); err != nil {
			fail("big Insert(%d): %v", i*2, err)
		}
		ref[i*2] = true
	}
	var wg sync.WaitGroup
	bad := make(chan int, 8)
	start := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for x := -10; x < 20010; x++ {
				if ok, _ := big.Lookup(x); ok != ref[x] {
					select {
					case bad <- x:
					default:
					}
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(bad)
	if x, ok := <-bad; ok {
		fail("concurrent Lookup(%d) mismatch", x)
	}
	if err := big.SelfCheck(); err != nil {
		fail("SelfCheck: %v", err)
	}
	fmt.Println("OK m=10000 Lookup 与参照一致(检查槽位恒2由测试钉住); 8 goroutine 并发只读一致; SelfCheck 通过")
}
