package api

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestDeterministic pins invariant 3: same input encodes to identical bytes.
func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, M := range []int{1, 2, 5, 7, 16, 255} {
		c, err := New(M)
		if err != nil {
			t.Fatal(err)
		}
		for trial := 0; trial < 20; trial++ {
			vs := make([]int64, rng.Intn(30))
			for i := range vs {
				vs[i] = int64(rng.Intn(500))
			}
			e1, _ := c.Encode(vs)
			e2, _ := c.Encode(vs)
			if !bytes.Equal(e1, e2) {
				t.Fatalf("M=%d %v: not deterministic", M, vs)
			}
		}
	}
}

// TestTruncatedNoPartial pins invariant 4: truncation fails wholesale,
// returns nil, and leaves the coder usable.
func TestTruncatedNoPartial(t *testing.T) {
	c, _ := New(5)
	full, _ := c.Encode([]int64{9, 100, 3})
	cases := [][]byte{
		full[:len(full)-1], // dropped tail byte
		{0xFF, 0xFF},       // unary never terminates
		{0xFE},             // unary ends but remainder bits run out
	}
	for i, bad := range cases {
		out, err := c.Decode(bad)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("case %d: err=%v, want ErrTruncated", i, err)
		}
		if out != nil {
			t.Fatalf("case %d: partial output %v left behind", i, out)
		}
	}
	back, err := c.Decode(full) // state intact: still decodes fine
	if err != nil || fmt.Sprint(back) != "[9 100 3]" {
		t.Fatalf("coder broken after rejection: %v %v", back, err)
	}
}

// TestDistinctErrors pins fault injection: three mutually distinct sentinels.
func TestDistinctErrors(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("M=0: %v", err)
	}
	if _, err := New(-7); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("M=-7: %v", err)
	}
	c, _ := New(5)
	if _, err := c.Encode([]int64{1, -1}); !errors.Is(err, ErrNegative) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := c.Decode([]byte{0xFF}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated: %v", err)
	}
	for _, pair := range [][2]error{
		{ErrInvalidParam, ErrNegative}, {ErrNegative, ErrTruncated}, {ErrInvalidParam, ErrTruncated},
	} {
		if errors.Is(pair[0], pair[1]) {
			t.Fatalf("%v and %v not distinct", pair[0], pair[1])
		}
	}
	if _, err := c.Encode([]int64{4}); err != nil { // usable after rejections
		t.Fatalf("coder broken after rejections: %v", err)
	}
}

// TestConcurrent pins race-freedom: N goroutines get serial-identical results.
func TestConcurrent(t *testing.T) {
	c, _ := New(5)
	seq := []int64{0, 2, 4, 5, 9, 1000, 123456}
	wantEnc, _ := c.Encode(seq)
	wantDec, _ := c.Decode(wantEnc)
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc, err := c.Encode(seq)
			if err != nil || !bytes.Equal(enc, wantEnc) {
				errs <- fmt.Sprintf("encode: %x %v", enc, err)
				return
			}
			dec, err := c.Decode(enc)
			if err != nil || fmt.Sprint(dec) != fmt.Sprint(wantDec) {
				errs <- fmt.Sprintf("decode: %v %v", dec, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestSelfCheck runs the built-in self-check of the four invariants.
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
