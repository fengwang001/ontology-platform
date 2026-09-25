package api

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

var exFreq = map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}

func TestLengthsAndCodes(t *testing.T) {
	c, err := New(exFreq)
	if err != nil {
		t.Fatal(err)
	}
	want := map[byte]string{'A': "0", 'B': "100", 'C': "101", 'D': "110", 'E': "111"}
	for s, w := range want {
		g, ok := c.CodeString(s)
		if !ok || g != w {
			t.Fatalf("symbol %c: got %q want %q", s, g, w)
		}
	}
	enc, _ := c.Encode([]byte("AABCCD"))
	if !bytes.Equal(enc, []byte{0x25, 0xB8}) {
		t.Fatalf("AABCCD -> %x, want 25b8", enc)
	}
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, m := range []int{1, 2, 5, 64, 256} {
		freq := map[byte]int{}
		for i := 0; i < m; i++ {
			freq[byte(i)] = 1 + r.Intn(500)
		}
		c, err := New(freq)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, 1, 7, 300} {
			msg := make([]byte, n)
			for i := range msg {
				msg[i] = byte(r.Intn(m))
			}
			enc, err := c.Encode(msg)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := c.Decode(enc, n)
			if err != nil || !bytes.Equal(dec, msg) {
				t.Fatalf("m=%d n=%d: round-trip failed: %v", m, n, err)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	c, _ := New(exFreq)
	cases := []struct {
		name string
		err  error
	}{
		{"unknown", func() error { _, e := c.Encode([]byte("AZ")); return e }()},
		{"truncated", func() error { _, e := c.Decode([]byte{0x25}, 6); return e }()},
		{"padding", func() error { _, e := c.Decode([]byte{0x25, 0xB9}, 6); return e }()},
		{"badfreq", func() error { _, e := New(map[byte]int{'A': -1}); return e }()},
	}
	want := []error{ErrUnknownSymbol, ErrTruncated, ErrIllegalPadding, ErrInvalidFreq}
	for i, tc := range cases {
		if !errors.Is(tc.err, want[i]) {
			t.Fatalf("%s: got %v, want %v", tc.name, tc.err, want[i])
		}
		for j, other := range want {
			if i != j && errors.Is(tc.err, other) {
				t.Fatalf("%s: %v must not match %v", tc.name, tc.err, other)
			}
		}
	}
	for _, bad := range []map[byte]int{{}, {'A': 0, 'B': 0}, {'A': -1}} {
		if _, err := New(bad); !errors.Is(err, ErrInvalidFreq) {
			t.Fatalf("freq %v: want ErrInvalidFreq, got %v", bad, err)
		}
	}
}

func TestFailureAtomic(t *testing.T) {
	c, _ := New(exFreq)
	before, _ := c.Encode([]byte("AABCCD"))
	if out, err := c.Encode([]byte("AZ")); err == nil || out != nil {
		t.Fatal("unknown symbol must fail with nil output")
	}
	if out, err := c.Decode([]byte{0x25}, 6); err == nil || out != nil {
		t.Fatal("truncated stream must fail with nil output")
	}
	if out, err := c.Decode([]byte{0x25, 0xB9}, 6); err == nil || out != nil {
		t.Fatal("illegal padding must fail with nil output")
	}
	if cc, err := New(map[byte]int{}); err == nil || cc != nil {
		t.Fatal("invalid freq must fail with nil codec")
	}
	after, _ := c.Encode([]byte("AABCCD"))
	dec, err := c.Decode(after, 6)
	if !bytes.Equal(before, after) || err != nil || !bytes.Equal(dec, []byte("AABCCD")) {
		t.Fatal("codec state changed after rejected calls")
	}
}

func TestConcurrent(t *testing.T) {
	c, _ := New(exFreq)
	msg := []byte("ABCDEABCDEAABCCD")
	serial, _ := c.Encode(msg)
	var wg sync.WaitGroup
	fail := make(chan string, 64)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				enc, err := c.Encode(msg)
				if err != nil || !bytes.Equal(enc, serial) {
					fail <- "encode"
					return
				}
				dec, err := c.Decode(enc, len(msg))
				if err != nil || !bytes.Equal(dec, msg) {
					fail <- "decode"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(fail)
	for f := range fail {
		t.Fatal("concurrent mismatch in", f)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
