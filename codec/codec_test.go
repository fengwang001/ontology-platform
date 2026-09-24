package codec_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/b32"
	"ontology/codec"
)

func input(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + n)
	}
	return b
}

func TestRoundTripAndShape(t *testing.T) {
	wantPad := map[int]int{0: 0, 1: 6, 2: 4, 3: 3, 4: 1}
	for n := 0; n <= 20; n++ {
		src := string(input(n))
		enc := codec.EncodeString(src)
		if len(enc) != 8*((n+4)/5) {
			t.Fatalf("n=%d: encoded length %d", n, len(enc))
		}
		if pad := len(enc) - len(strings.TrimRight(enc, "=")); pad != wantPad[n%5] {
			t.Fatalf("n=%d: padding %d", n, pad)
		}
		dec, err := codec.DecodeString(enc)
		if err != nil || dec != src {
			t.Fatalf("n=%d: round trip failed", n)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name, in string
		want     error
	}{
		{"invalid char", "ABC!EFGH", b32.ErrInvalidChar},
		{"padding middle", "AB=CDEF=", b32.ErrPaddingPosition},
		{"padding count 2", "AAAAAA==", b32.ErrPaddingCount},
		{"padding count 5", "AAA=====", b32.ErrPaddingCount},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.in
			got, err := codec.DecodeString(tt.in)
			if !errors.Is(err, tt.want) || got != "" || tt.in != before {
				t.Fatalf("err=%v got=%q", err, got)
			}
		})
	}
	if codec.EncodeString("hi") != "NBUQ====" {
		t.Fatal("codec unusable after rejections")
	}
}

func TestGroupCounts(t *testing.T) {
	c := b32.New()
	for _, n := range []int{1000, 100000} {
		before := c.Groups()
		c.Encode(input(n))
		if got := c.Groups() - before; got != int64((n+4)/5) {
			t.Fatalf("n=%d: groups %d want %d", n, got, (n+4)/5)
		}
	}
}

func TestConcurrent(t *testing.T) {
	src := string(input(64))
	want := codec.EncodeString(src)
	var wg sync.WaitGroup
	fails := make(chan string, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc := codec.EncodeString(src)
			dec, err := codec.DecodeString(enc)
			if enc != want || err != nil || dec != src {
				fails <- "mismatch"
			}
		}()
	}
	wg.Wait()
	if len(fails) > 0 {
		t.Fatal("byte mismatch under concurrency")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := codec.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
