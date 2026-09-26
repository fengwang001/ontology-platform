package dist

import (
	"math/rand"
	"strings"
	"testing"
)

// naive is the reference full-table DP (no band).
func naive(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			best := prev[j] + 1
			if v := curr[j-1] + 1; v < best {
				best = v
			}
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			if v := prev[j-1] + cost; v < best {
				best = v
			}
			curr[j] = best
		}
		prev = curr
	}
	return prev[len(b)]
}

func randStr(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(4))
	}
	return string(b)
}

func mutate(a string, r *rand.Rand, edits int) string {
	b := []byte(a)
	for e := 0; e < edits && len(b) > 0; e++ {
		p := r.Intn(len(b))
		switch r.Intn(3) {
		case 0:
			b = append(b, 0)
			copy(b[p+1:], b[p:])
			b[p] = byte('a' + r.Intn(4))
		case 1:
			b = append(b[:p], b[p+1:]...)
		default:
			b[p] = byte('a' + r.Intn(4))
		}
	}
	return string(b)
}

func TestDistanceMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	cases := []struct{ n, m, k int }{{0, 0, 0}, {0, 5, 5}, {3, 4, 2}, {10, 10, 3}, {20, 15, 4}, {40, 40, 6}}
	for _, c := range cases {
		for trial := 0; trial < 30; trial++ {
			a, b := randStr(r, c.n), randStr(r, c.m)
			if trial%2 == 0 {
				b = mutate(a, r, c.k+1)
			}
			want := naive(a, b)
			got, err := Distance(a, b, c.k)
			if want <= c.k && (err != nil || got != want) {
				t.Fatalf("(%q,%q) k=%d: want %d, got %d, %v", a, b, c.k, want, got, err)
			}
			if want > c.k && err != ErrExceedsCap {
				t.Fatalf("(%q,%q) k=%d: want ErrExceedsCap, got %d, %v", a, b, c.k, got, err)
			}
		}
	}
}

func TestCapBoundary(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for trial := 0; trial < 60; trial++ {
		a := randStr(r, 1+r.Intn(12))
		b := mutate(a, r, r.Intn(9))
		d := naive(a, b)
		for k := 0; k <= d+1; k++ {
			got, err := Distance(a, b, k)
			if k < d && err != ErrExceedsCap {
				t.Fatalf("(%q,%q) k=%d d=%d: want ErrExceedsCap, got %d,%v", a, b, k, d, got, err)
			}
			if k >= d && (err != nil || got != d) {
				t.Fatalf("(%q,%q) k=%d d=%d: got %d,%v", a, b, k, d, got, err)
			}
		}
	}
}

func TestBandCellCount(t *testing.T) {
	const k = 3
	for _, n := range []int{100, 300, 1000, 3000, 10000} {
		a := strings.Repeat("a", n)
		b := a[:n/2] + "bb" + a[n/2:n-2]
		if _, err := Distance(a, b, k); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		cells := lastCells.Load()
		limit := int64((2*k+1)*n + 4*k*k) // linear in n, not n^2
		if cells > limit {
			t.Fatalf("n=%d: cells=%d exceeds band limit %d", n, cells, limit)
		}
	}
}
