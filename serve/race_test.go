package serve_test

import (
	"bytes"
	"sync"
	"testing"

	"ontology/serve"
	"ontology/source"
)

// TestConcurrentIndependentAssemblers：多个 goroutine 各自组装不同的响应，
// 在 -race 下必须干净且互不干扰。
func TestConcurrentIndependentAssemblers(t *testing.T) {
	const goroutines = 24
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			data := makeData(64)
			// 每个 goroutine 使用不同区间与不同短写粒度。
			start := g % 30
			header := "bytes=" + itoa(start) + "-" + itoa(start+9)

			f := source.NewFlaky(source.NewMemory(data))
			f.MaxChunk = 3
			f.ShortCalls = map[int]bool{0: true}

			a := serve.New(serve.Config{})
			if err := a.Build(header, f); err != nil {
				errs <- err
				return
			}
			got := drainAll(t, a)
			if !bytes.Equal(got, data[start:start+10]) {
				errs <- errMismatch(g)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type mismatchErr int

func (e mismatchErr) Error() string { return "concurrent assembler data mismatch" }

func errMismatch(g int) error { return mismatchErr(g) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
