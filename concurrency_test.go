package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentLocate 多协程并发 Locate 与 Add/Remove 交错：
// 不得 panic、不得返回空串，返回值必须是曾经加入环的真实节点。
// 环上始终保留 5 个永不删除的基础节点，保证 Locate 不会遇到空环。
// 用 go test -race 运行以检测数据竞争。
func TestConcurrentLocate(t *testing.T) {
	r := New()
	base := nodeIDs(5)
	for _, id := range base {
		if err := r.Add(id, 50); err != nil {
			t.Fatal(err)
		}
	}

	// universe 记录所有真实存在过的节点 ID。
	universe := make(map[string]struct{})
	for _, id := range base {
		universe[id] = struct{}{}
	}
	for i := 0; i < 10; i++ {
		universe[fmt.Sprintf("extra-%02d", i)] = struct{}{}
	}

	keys := genKeys(2000)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan string, 64)

	// 读 goroutine：并发 Locate。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				owner, err := r.Locate(keys[(i+off)%len(keys)])
				if err != nil {
					select {
					case errs <- fmt.Sprintf("Locate 出错: %v", err):
					default:
					}
					return
				}
				if owner == "" {
					select {
					case errs <- "Locate 返回空串":
					default:
					}
					return
				}
				if _, ok := universe[owner]; !ok {
					select {
					case errs <- fmt.Sprintf("Locate 返回未知节点 %q", owner):
					default:
					}
					return
				}
			}
		}(w)
	}

	// 写 goroutine：反复增删额外节点。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("extra-%02d", i%10)
			if i%2 == 0 {
				_ = r.Add(id, 20)
			} else {
				_ = r.Remove(id)
			}
		}
		close(stop)
	}()

	wg.Wait()
	select {
	case msg := <-errs:
		t.Fatal(msg)
	default:
	}
}
