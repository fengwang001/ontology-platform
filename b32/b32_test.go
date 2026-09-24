package b32

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func input(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*37 + n)
	}
	return b
}
func TestRoundTrip(t *testing.T) {
	legalPad := map[int]bool{0: true, 1: true, 3: true, 4: true, 6: true}
	for n := 0; n <= 20; n++ {
		src := input(n)
		enc := Encode(src)
		if len(enc) != 8*((n+4)/5) {
			t.Fatalf("n=%d: encoded length %d", n, len(enc))
		}
		pad := 0
		for pad < len(enc) && enc[len(enc)-1-pad] == '=' {
			pad++
		}
		if !legalPad[pad] {
			t.Fatalf("n=%d: illegal padding %d", n, pad)
		}
		dec, err := Decode(enc)
		if err != nil || !bytes.Equal(dec, src) {
			t.Fatalf("n=%d: roundtrip mismatch, err=%v", n, err)
		}
	}
}

func TestErrors(t *testing.T) {
	if ErrInvalidChar == ErrPaddingPosition || ErrPaddingPosition == ErrPaddingCount ||
		ErrInvalidChar == ErrPaddingCount {
		t.Fatal("sentinel errors must be distinct")
	}
	cases := []struct {
		in  string
		err error
	}{
		{"AAAAAAA!", ErrInvalidChar},
		{"AAA=AAAA", ErrPaddingPosition},
		{"AAAAAA==", ErrPaddingCount},
		{"AAA=====", ErrPaddingCount},
	}
	for _, c := range cases {
		before := c.in
		b, err := Decode(c.in)
		if !errors.Is(err, c.err) || b != nil || c.in != before {
			t.Fatalf("%q: got (%v, %v)", c.in, b, err)
		}
	}
	if _, err := Decode(Encode(input(7))); err != nil {
		t.Fatalf("codec unusable after rejection: %v", err)
	}
}

func TestGroups(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		start := groups.Load()
		Encode(input(n))
		if got := groups.Load() - start; got != int64((n+4)/5) {
			t.Fatalf("n=%d: processed %d groups, want %d", n, got, (n+4)/5)
		}
	}
}

func TestConcurrent(t *testing.T) {
	inputs := [][]byte{input(0), input(1), input(7), input(20), input(1000)}
	enc := make([]string, len(inputs))
	for i, b := range inputs {
		enc[i] = Encode(b)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, b := range inputs {
				d, err := Decode(enc[i])
				if Encode(b) != enc[i] || err != nil || !bytes.Equal(d, b) {
					errs <- "mismatch"
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
