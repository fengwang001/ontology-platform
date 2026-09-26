package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/est"
)

var seq7 = [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}

func mustNew(t *testing.T, m int) *LogLog {
	c, err := New(m)
	if err != nil {
		t.Fatalf("New(%d): %v", m, err)
	}
	return c
}

func add(t *testing.T, l *LogLog, b, z int) {
	if err := l.Add(b, z); err != nil {
		t.Fatal(err)
	}
}

func TestAddMonotonic(t *testing.T) {
	for _, m := range []int{1, 2, 8, 64} {
		c := mustNew(t, m)
		prev := c.Registers()
		for i := 0; i < 200; i++ {
			add(t, c, i%m, (i*7)%20)
			cur := c.Registers()
			for j := range cur {
				if cur[j] < prev[j] {
					t.Fatalf("m=%d: reg[%d] decreased %d -> %d", m, j, prev[j], cur[j])
				}
			}
			prev = cur
		}
	}
}

func TestSingleElementExact(t *testing.T) {
	for _, c := range []struct{ m, bucket, z int }{{8, 0, 0}, {8, 3, 4}, {8, 7, 9}, {16, 15, 1}} {
		l := mustNew(t, c.m)
		add(t, l, c.bucket, c.z)
		for j, v := range l.Registers() {
			if want := (c.z + 1) * b2i(j == c.bucket); v != want {
				t.Fatalf("%+v: reg[%d]=%d, want %d", c, j, v, want)
			}
		}
	}
}

func TestNaiveReplay(t *testing.T) {
	for _, trial := range []int{0, 1, 2} {
		m := 8 << trial
		l, naive := mustNew(t, m), make([]int, m)
		rng := rand.New(rand.NewSource(int64(trial) + 1))
		n := 7
		if trial > 0 {
			n = 500
		}
		for i := 0; i < n; i++ {
			b, z := rng.Intn(m), rng.Intn(30)
			add(t, l, b, z)
			if r := z + 1; r > naive[b] {
				naive[b] = r
			}
		}
		sum := 0
		for j, v := range l.Registers() {
			if v != naive[j] {
				t.Fatalf("m=%d: reg[%d]=%d, naive=%d", m, j, v, naive[j])
			}
			sum += naive[j]
		}
		if got, want := l.Estimate(), est.Alpha(m)*float64(m)*math.Exp2(float64(sum)/float64(m)); got != want {
			t.Fatalf("m=%d: Estimate()=%v, want %v", m, got, want)
		}
	}
}

func TestRejectedLeavesNoTrace(t *testing.T) {
	for _, m := range []int{0, -4, 3, 5, 6, 7, 12, 100} {
		if _, err := New(m); !errors.Is(err, ErrInvalidM) {
			t.Fatalf("New(%d)=%v", m, err)
		}
	}
	l := mustNew(t, 8)
	for _, p := range seq7 {
		add(t, l, p[0], p[1])
	}
	before := l.Registers()
	bads := [][2]int{{-1, 0}, {8, 0}, {100, 5}, {0, -1}, {7, -2}}
	wants := []error{ErrBucketOutOfRange, ErrBucketOutOfRange, ErrBucketOutOfRange, ErrInvalidZ, ErrInvalidZ}
	for i, b := range bads {
		if err := l.Add(b[0], b[1]); !errors.Is(err, wants[i]) {
			t.Fatalf("Add(%d,%d)=%v, want %v", b[0], b[1], err, wants[i])
		}
	}
	for j, v := range l.Registers() {
		if v != before[j] {
			t.Fatalf("rejected add changed reg[%d]: %d -> %d", j, before[j], v)
		}
	}
	if err := l.Add(2, 9); err != nil || l.Registers()[2] != 10 { // still usable
		t.Fatalf("after rejection: err=%v reg[2]=%d", err, l.Registers()[2])
	}
}
func TestSentinelErrorsDistinct(t *testing.T) {
	if ErrInvalidM == ErrBucketOutOfRange || ErrBucketOutOfRange == ErrInvalidZ || ErrInvalidM == ErrInvalidZ {
		t.Fatal("sentinel errors must be mutually distinct")
	}
}

func TestConcurrentEstimateAndSelfCheck(t *testing.T) {
	for _, m := range []int{1, 2, 8, 1024} {
		if err := mustNew(t, m).SelfCheck(); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
	}
	l := mustNew(t, 64)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		add(t, l, rng.Intn(64), rng.Intn(40))
	}
	vals := make([]float64, 32)
	var wg sync.WaitGroup
	for i := range vals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = l.Registers()
			if err := l.SelfCheck(); err != nil {
				t.Error(err)
			}
			vals[i] = l.Estimate()
		}(i)
	}
	wg.Wait()
	for i, v := range vals {
		if v != vals[0] {
			t.Fatalf("goroutine %d got %v, want %v", i, v, vals[0])
		}
	}
}
