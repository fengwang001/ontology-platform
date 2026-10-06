package framing

import (
	"runtime"
	"sync"
	"testing"
)

// TestConcurrentIndependentConnections feeds many parser instances from many
// goroutines under -race; each parser is independent and must produce exactly
// the same event stream as a serial reference run.
func TestConcurrentIndependentConnections(t *testing.T) {
	cfg := Config{MaxHeaderBytes: 1024, MaxBodyBytes: 64}
	streams := [][]byte{
		[]byte("POST /a HTTP/1.1\r\nContent-Length: 3\r\n\r\nabcGET /b HTTP/1.1\r\n\r\n"),
		[]byte("POST /c HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n0\r\n\r\n"),
		[]byte("GET /bad HTTP/2.0\r\n\r\nGET /never HTTP/1.1\r\n\r\n"),
	}
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU()*2; w++ {
		for s, data := range streams {
			wg.Add(1)
			go func(s int, data []byte) {
				defer wg.Done()
				want, _ := naiveOracle(cfg, data)
				got := runAll(cfg, data)
				if !eventsEqual(want, got) {
					t.Errorf("worker stream %d mismatch\n%v\n%v", s, want, got)
				}
			}(s, append([]byte(nil), data...))
		}
	}
	wg.Wait()
}

// TestConcurrentSameConnection sends chunks concurrently into one parser.
// The result must equal one of the serial orderings of those chunks; here
// each chunk is itself a complete, independent request so any serial order
// yields one RequestComplete...RequestEnd per chunk.
func TestConcurrentSameConnection(t *testing.T) {
	p := NewParser(DefaultConfig())
	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []Event
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := p.Feed([]byte("GET / HTTP/1.1\r\n\r\n"))
			mu.Lock()
			all = append(all, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	headers, ends := 0, 0
	for _, e := range all {
		switch e.Kind {
		case EventHeaderComplete:
			headers++
		case EventRequestEnd:
			ends++
		}
	}
	if headers != workers || ends != workers {
		t.Fatalf("headers=%d ends=%d", headers, ends)
	}
}

// TestSteadyStateZeroAlloc proves two properties:
//
//  1. Once a request header is complete, feeding body bytes into a
//     caller-provided event buffer performs zero heap allocations
//     (constant per-byte cost, independent of already-processed byte count).
//  2. The total allocation to drain a body does not grow with body length:
//     draining 1 KiB and 1 MiB report identical allocation counts.
func TestSteadyStateZeroAlloc(t *testing.T) {
	results := map[int]float64{}
	for _, size := range []int{1024, 1 << 20} {
		cfg := Config{MaxHeaderBytes: 1024, MaxBodyBytes: uint64(size)}
		head := []byte("POST / HTTP/1.1\r\nContent-Length: " + itoa(size) + "\r\n\r\n")
		buf := make([]Event, 0, 16)
		payload := make([]byte, size)

		// Pre-position N parsers at the body start so the timed region only
		// exercises the steady-state body transition (header parsing and
		// parser construction are intentionally excluded).
		const runs = 16
		parsers := make([]*Parser, runs)
		for k := range parsers {
			parsers[k] = NewParser(cfg)
			parsers[k].Feed(head)
		}
		k := 0
		allocs := testing.AllocsPerRun(8, func() {
			_ = parsers[k].FeedInto(buf[:0], payload)
			k++
		})
		results[size] = allocs
	}
	if results[1024] != 0 || results[1<<20] != results[1024] {
		t.Fatalf("unexpected allocation behavior: %v", results)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func BenchmarkFixedBody1MB(b *testing.B) {
	const size = 1 << 20
	cfg := Config{MaxHeaderBytes: 1024, MaxBodyBytes: size}
	data := []byte("POST / HTTP/1.1\r\nContent-Length: " + itoa(size) + "\r\n\r\n")
	data = append(data, make([]byte, size)...)
	buf := make([]Event, 0, 16)
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		p := NewParser(cfg)
		p.Feed(data[:len(data)-size])
		p.FeedInto(buf[:0], data[len(data)-size:])
	}
}

// BenchmarkBodySteadyState measures only the per-body-byte steady-state
// transition (header and connection setup excluded): it must report
// 0 B/op and 0 allocs/op.
func BenchmarkBodySteadyState(b *testing.B) {
	const size = 1 << 20
	cfg := Config{MaxHeaderBytes: 1024, MaxBodyBytes: uint64(b.N) * size}
	p := NewParser(cfg)
	p.Feed([]byte("POST / HTTP/1.1\r\nContent-Length: " + itoa(b.N*size) + "\r\n\r\n"))
	buf := make([]Event, 0, 16)
	chunk := make([]byte, size)
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		_ = p.FeedInto(buf[:0], chunk)
	}
}
