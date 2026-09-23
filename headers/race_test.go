package headers_test

import (
	"slices"
	"sync"
	"testing"

	"ontology/headers"
)

// 多 goroutine 并发查询同一集合：go test -race 干净，结果与串行一致。
func TestConcurrentReads(t *testing.T) {
	s, err := headers.Parse([]byte(
		"B: 1\r\nA: 2\r\nB: 3\r\nX-Multi: a\r\nX-Multi: b\r\n\r\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantAll := s.GetAll("B")
	wantSingle, _ := s.Get("X-Multi")
	wantBytes := s.Bytes()
	wantLen := s.Len()

	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if got := s.GetAll("B"); !slices.Equal(got, wantAll) {
					errs <- "GetAll 不一致"
				}
				if got, _ := s.Get("X-Multi"); got != wantSingle {
					errs <- "Get 不一致"
				}
				if string(s.Bytes()) != string(wantBytes) {
					errs <- "Bytes 不一致"
				}
				if s.Len() != wantLen || s.Count("B") != 2 || !s.Has("A") {
					errs <- "计数查询不一致"
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
