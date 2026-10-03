package mailthread

import (
	"fmt"
	"sync"
	"testing"
)

// 用非导出计数器 scans 证明：候选查找考察的线程数不超过同主题
// 线程数，与总线程数无关（总线程 100 与 10000 两档对照）。
func TestCandidateScanCount(t *testing.T) {
	const sameSubject = 5
	for _, total := range []int{100, 10000} {
		th, err := New(100, MaxNodes)
		if err != nil {
			t.Fatal(err)
		}
		// 5 个规范化主题为 "S" 的线程。
		for i := 0; i < sameSubject; i++ {
			if _, err := th.Add(fmt.Sprintf("s%d", i), nil, "", "S", 10); err != nil {
				t.Fatal(err)
			}
		}
		// 其余为互不相同、且与 "S" 不同主题的线程。
		for i := sameSubject; i < total; i++ {
			if _, err := th.Add(fmt.Sprintf("f%d", i), nil, "", fmt.Sprintf("F%d", i), 10); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(th.Threads()); got != total {
			t.Fatalf("总线程数=%d, want %d", got, total)
		}
		before := th.scans
		r, err := th.Add("probe", nil, "", "Re: S", 20)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path != PathSubject {
			t.Fatalf("总线程 %d: 途径=%s, want subject", total, r.Path)
		}
		scanned := th.scans - before
		t.Logf("总线程=%d 同主题线程=%d 考察线程数=%d", total, sameSubject, scanned)
		if scanned != sameSubject {
			t.Fatalf("总线程 %d: 考察线程数=%d, want %d（与总线程数无关）",
				total, scanned, sameSubject)
		}
	}
}

// 并发调用烟测试（配合 -race）：结果等价于某个串行顺序，
// 且不变量保持：每个线程至少一封真实邮件、编号互不相同且等于根的 id。
func TestConcurrent(t *testing.T) {
	th, err := New(1000, MaxNodes)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("w%d-m%d", g, i)
				var refs []string
				if i > 0 {
					refs = []string{fmt.Sprintf("w%d-m%d", g, i-1)}
				}
				if _, err := th.Add(id, refs, "", fmt.Sprintf("Re: Topic%d", g), int64(i)); err != nil {
					t.Errorf("Add(%s): %v", id, err)
					return
				}
				th.Threads()
				th.ThreadOf(id)
			}
		}(g)
	}
	wg.Wait()

	th.mu.Lock()
	for id, th2 := range th.threads {
		if th2.realCnt < 1 {
			t.Fatalf("线程 %s 没有真实邮件", id)
		}
		if id != th2.root.id {
			t.Fatalf("线程编号 %s != 根 id %s", id, th2.root.id)
		}
		if th2.root.th != th2 {
			t.Fatalf("线程 %s 的根不属于该线程", id)
		}
	}
	if len(th.nodes) > MaxNodes {
		t.Fatalf("节点数 %d 超过上限", len(th.nodes))
	}
	threadCnt := len(th.threads)
	th.mu.Unlock()
	// 每个 worker 的邮件经 refs 链连通，应各成一个线程。
	if threadCnt != workers {
		t.Fatalf("线程数=%d, want %d", threadCnt, workers)
	}
}
