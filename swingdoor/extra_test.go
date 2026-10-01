package swingdoor

import (
	"math/rand"
	"sync"
	"testing"
)

func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		e := rng.Int63n(6)
		n := 2 + rng.Intn(40)
		pts := make([]Sample, n)
		ts := int64(-50)
		for i := range pts {
			ts += int64(rng.Intn(3) + 1)
			pts[i] = Sample{T: ts, V: int64(rng.Intn(21) - 10)}
		}
		run(t, e, pts...)
	}
}

// 大范围数值：分子/分母差均可达 2e9，交叉乘积超过 int64，由 big.Int 精确处理。
func TestLargeCoordinates(t *testing.T) {
	got := run(t, 4_000_000_000,
		Sample{-1_000_000_000, -1_000_000_000},
		Sample{0, 1_000_000_000},
		Sample{1_000_000_000, 1_000_000_000},
	)
	want := []Sample{
		{-1_000_000_000, -1_000_000_000},
		{1_000_000_000, 1_000_000_000},
	}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 并发写入、查询、关闭：互斥保证可线性化；结果满足朴素校验。
func TestConcurrentWriteQueryClose(t *testing.T) {
	const n = 200
	c, _ := New(3)

	var wg sync.WaitGroup
	writeDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(writeDone)
		for i := int64(0); i < n; i++ {
			if err := c.Write(i, (i*7)%11-5); err != nil {
				t.Errorf("write %d: %v", i, err)
				return
			}
		}
	}()

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-writeDone:
					return
				default:
					a := c.Archives()
					for i := 1; i < len(a); i++ {
						if a[i].T <= a[i-1].T {
							t.Errorf("non-increasing archives: %v", a)
							return
						}
					}
				}
			}
		}()
	}

	wg.Wait()

	input := make([]Sample, n)
	for i := range input {
		input[i] = Sample{T: int64(i), V: (int64(i)*7)%11 - 5}
	}

	// Close 可能与读者并发，多试一次安全关闭。
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != ErrClosed {
		t.Fatalf("second close: %v", err)
	}

	got := c.Archives()
	naiveVerify(t, 3, input, got)

	// 与串行重放逐位一致。
	ref, _ := New(3)
	mustWrite(t, ref, input...)
	if err := ref.Close(); err != nil {
		t.Fatal(err)
	}
	if !samplesEq(got, ref.Archives()) {
		t.Fatalf("concurrent result differs from serial replay: %v vs %v", got, ref.Archives())
	}
}
