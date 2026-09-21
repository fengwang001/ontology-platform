package bloom

import (
	"bytes"
	"sync"
	"testing"
)

// 多协程并发 Add 不得丢置位：所有元素事后必须全部命中（无假阴性），
// 且位数组与串行插入逐字节相同。用 -race 跑以检测数据竞争。
func TestConcurrentAdd(t *testing.T) {
	const (
		workers   = 8
		perWorker = 10000
	)
	f, err := New(workers*perWorker, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				f.Add(elem("c", base+i))
			}
		}(w * perWorker)
	}
	wg.Wait()

	for i := 0; i < workers*perWorker; i++ {
		if !f.MayContain(elem("c", i)) {
			t.Fatalf("false negative at %d after concurrent Add", i)
		}
	}

	serial, err := New(workers*perWorker, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < workers*perWorker; i++ {
		serial.Add(elem("c", i))
	}
	if !bytes.Equal(f.Bytes(), serial.Bytes()) {
		t.Fatal("concurrent Add lost bits: differs from serial insert")
	}
}

// 并发进行中的 MayContain 与 Bytes 不得撕裂：已 Add 完成的元素
// 在并发读取期间必须始终可见，Bytes 必须是完整快照。
func TestConcurrentReadWhileWrite(t *testing.T) {
	const total = 20000
	f, err := New(total, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = f.MayContain(elem("r", 0))
				snap := f.Bytes()
				if len(snap) == 0 {
					t.Error("Bytes returned empty snapshot")
					return
				}
			}
		}()
	}
	f.Add(elem("r", 0))
	for i := 0; i < total; i++ {
		f.Add(elem("w", i))
	}
	// 已 Add 完成的元素在并发读取进行中必须可见。
	if !f.MayContain(elem("r", 0)) {
		t.Fatal("added element not visible during concurrent reads")
	}
	close(done)
	wg.Wait()
}
