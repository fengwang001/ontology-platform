package headers

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentReads 多 goroutine 并发只读：go test -race 干净且结果与串行一致。
func TestConcurrentReads(t *testing.T) {
	s, err := Parse([]byte("A: 1\r\nA: 2\r\nB: x\r\nC: \r\n\r\n"), testRegistry(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	serial := make([]string, 0, 800)
	for i := 0; i < 800; i++ {
		v, _ := s.Get("A")
		serial = append(serial, fmt.Sprintf("%s|%v|%d|%d|%d|%v|%v",
			v, s.GetAll("A"), s.Len(), s.Count("A"), s.ByteLen(), s.Has("B"), s.Normalized()))
	}
	parallel := make([]string, 800)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g * 100; i < g*100+100; i++ {
				v, _ := s.Get("A")
				parallel[i] = fmt.Sprintf("%s|%v|%d|%d|%d|%v|%v",
					v, s.GetAll("A"), s.Len(), s.Count("A"), s.ByteLen(), s.Has("B"), s.Normalized())
			}
		}(g)
	}
	wg.Wait()
	for i := range serial {
		if serial[i] != parallel[i] {
			t.Fatalf("并发与串行结果不一致 at %d: %q vs %q", i, serial[i], parallel[i])
		}
	}
}

// TestConcurrentReadWrite 写操作互斥：并发写不 panic、-race 干净，
// 且写后集合仍可一致查询（写并发安全级别见包文档：写者互斥）。
func TestConcurrentReadWrite(t *testing.T) {
	s := New(nil, Config{MaxHeaders: 4096})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = s.Add(fmt.Sprintf("H-%d", g), "v")
				_, _ = s.Get(fmt.Sprintf("H-%d", g))
			}
		}(g)
	}
	wg.Wait()
	if s.Len() != 400 {
		t.Fatalf("并发写后条数应为 400: %d", s.Len())
	}
	for g := 0; g < 4; g++ {
		if got := s.Count(fmt.Sprintf("H-%d", g)); got != 100 {
			t.Errorf("H-%d 出现次数 %d, want 100", g, got)
		}
	}
}
