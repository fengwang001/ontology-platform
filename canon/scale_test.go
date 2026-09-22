package canon

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// sizedURL builds a URL of about size bytes with real work in every
// component: escapes in path segments and query parameters.
func sizedURL(size int) string {
	var b strings.Builder
	b.WriteString("http://example.com")
	i := 0
	for b.Len() < size {
		fmt.Fprintf(&b, "/seg%d%%41x", i)
		i++
	}
	return b.String()
}

func measureScans(n *Normalizer, url string) int64 {
	before := n.ScanCount()
	if _, err := n.Normalize(url); err != nil {
		panic(err)
	}
	return n.ScanCount() - before
}

func TestScanCountIsLinear(t *testing.T) {
	n := New(Config{})
	small, big := sizedURL(1<<10), sizedURL(1<<16)
	s1 := measureScans(n, small)
	s2 := measureScans(n, big)
	ratio := float64(s2) / float64(s1)
	t.Logf("L=%d scans=%d; L=%d scans=%d; ratio=%.1f (len ratio=%.1f)",
		len(small), s1, len(big), s2, ratio, float64(len(big))/float64(len(small)))
	// Linear growth tracks the 64x length ratio; quadratic would be ~4096x.
	if ratio < 30 || ratio > 130 {
		t.Fatalf("scan growth %.1fx is not linear in input length", ratio)
	}
}

func TestConcurrentMatchesSerial(t *testing.T) {
	n := New(Config{Mode: ModeSorted})
	r := randURLs(64)
	serial := make([]string, len(r))
	for i, u := range r {
		s, err := n.Normalize(u)
		if err != nil {
			t.Fatalf("%q: %v", u, err)
		}
		serial[i] = s
	}
	var wg sync.WaitGroup
	fail := make(chan string, 1)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for iter := 0; iter < 50; iter++ {
				u := r[(g+iter)%len(r)]
				got, err := n.Normalize(u)
				if err != nil {
					select {
					case fail <- err.Error():
					default:
					}
					return
				}
				if want := serial[(g+iter)%len(r)]; got != want {
					select {
					case fail <- fmt.Sprintf("%q: %q != %q", u, got, want):
					default:
					}
					return
				}
			}
		}(g)
	}
	wg.Wait()
	select {
	case msg := <-fail:
		t.Fatal(msg)
	default:
	}
}

func randURLs(k int) []string {
	out := make([]string, k)
	for i := range out {
		out[i] = fmt.Sprintf(
			"HTTP://ExAmPLE.com.:080/p%d/../q%%41?b=%d&a=%%2f&a=1#f", i, i)
	}
	return out
}
