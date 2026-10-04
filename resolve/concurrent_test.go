package resolve

import (
	"sync"
	"testing"
)

// TestConcurrentSafety 对同一设备并发操作，要求无竞态、待定计数始终不越界、
// 所有输出的载荷唯一（每条记录恰输出一次）。
func TestConcurrentSafety(t *testing.T) {
	r := New()
	const pmax = 1_000_000
	if err := r.Register(1, pmax); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	run := func(fn func() ([]Emit, error)) {
		defer wg.Done()
		out, err := fn()
		if err != nil {
			return
		}
		mu.Lock()
		for _, e := range out {
			p := e.Payload.(string)
			if seen[p] {
				t.Errorf("payload %q emitted twice", p)
			}
			seen[p] = true
		}
		mu.Unlock()
	}
	for g := 0; g < 8; g++ {
		g := g
		wg.Add(2)
		go run(func() ([]Emit, error) {
			return r.Record(1, int64(g%3+1), int64(g*7), fmtP(g))
		})
		go run(func() ([]Emit, error) {
			return r.Sync(1, int64(g%3+1), int64(g*7), int64(1000+g*13))
		})
	}
	wg.Wait()
	if d := r.devs[1]; d.pending > pmax {
		t.Fatalf("pending %d > pmax", d.pending)
	}
}

func fmtP(g int) string {
	return "p" + itoa(g)
}

func itoa(g int) string {
	if g == 0 {
		return "0"
	}
	var b []byte
	for g > 0 {
		b = append([]byte{byte('0' + g%10)}, b...)
		g /= 10
	}
	return string(b)
}
