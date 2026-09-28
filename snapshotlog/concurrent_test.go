package snapshotlog

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentWritesSnapshotPoll 覆盖写入与快照、轮询的真实并发：
// 快照期间持续写入；完成后多个轮询协程并发追赶；最终视图必须与源表
// 范围内状态逐键一致，且不含任何范围外键、不重复不回退。
func TestConcurrentWritesSnapshotPoll(t *testing.T) {
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})))
	r := KeyRange{Start: 0, End: 49}

	// 预置一些旧数据，其中夹一个范围外键。
	for k := int64(0); k < 60; k++ {
		src.Put(k, "init")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 多个写入协程：对范围内键交替 Put/Delete，并持续写范围外键。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := int64((i*7 + id) % 50) // 始终落在 [0,49]
				if i%3 == 0 {
					src.Delete(k)
				} else {
					src.Put(k, fmt.Sprintf("w%d-%d", id, i))
				}
				// 范围外键的任何写入都不得进入视图。
				src.Put(100+int64(id), "out")
				i++
			}
		}(w)
	}

	// 与写入并发地走完三步（开始后稍等，让快照期间确有写入）。
	if _, err := x.BeginSnapshot(r); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, _, err := x.ReadSnapshot(r); err != nil {
		t.Fatalf("Read: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := x.FinishSnapshot(r); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// 完成后多个轮询协程并发追赶，同时有协程持续读视图。
	var seenSeq sync.Map // 记录所有已输出序号，断言全局不重复
	var pollWG sync.WaitGroup
	for p := 0; p < 4; p++ {
		pollWG.Add(1)
		go func() {
			defer pollWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				evs, err := x.Poll()
				if err != nil {
					t.Errorf("Poll: %v", err)
					return
				}
				for _, e := range evs {
					if _, loaded := seenSeq.LoadOrStore(e.Entry.Seq, true); loaded {
						t.Errorf("序号 %d 被重复输出", e.Entry.Seq)
					}
				}
				_ = x.View() // 并发读取不得 panic，且始终返回自洽副本
			}
		}()
	}

	// 运行一段时间后停止写入；范围外键不推进该范围的处理位置，
	// 因此以“视图与源表范围内快照一致”为追平判据，而非全局最大序号。
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	viewEqual := func() bool {
		v := x.View()
		w := src.snapshot(r).rows
		if len(v) != len(w) {
			return false
		}
		for k, val := range w {
			if v[k] != val {
				return false
			}
		}
		return true
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if evs, err := x.Poll(); err != nil {
			t.Fatalf("排空轮询: %v", err)
		} else if len(evs) == 0 && viewEqual() {
			break
		}
	}
	pollWG.Wait() // 轮询协程观察到 stop 后退出

	view := x.View()
	want := src.snapshot(r).rows
	if len(view) != len(want) {
		t.Fatalf("视图行数 %d 与源表范围内行数 %d 不一致", len(view), len(want))
	}
	for k, v := range want {
		if view[k] != v {
			t.Fatalf("键 %d 视图值=%q 源表值=%q", k, view[k], v)
		}
	}
	for k := range view {
		if !r.Contains(k) {
			t.Fatalf("范围外键 %d 泄漏进视图", k)
		}
	}
}
