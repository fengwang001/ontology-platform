package b64_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/b64"
)

func textbookEncode(src []byte) []byte {
	dst := make([]byte, ((len(src)+2)/3)*4)
	for si, di := 0, 0; si < len(src); si, di = si+3, di+4 {
		m := len(src) - si
		v := uint(src[si]) << 16
		if m > 1 {
			v |= uint(src[si+1]) << 8
		}
		if m > 2 {
			v |= uint(src[si+2])
		}
		for k := 0; k < 4; k++ {
			dst[di+k] = b64.Alphabet[(v>>uint(18-6*k))&63]
		}
		if m < 3 {
			dst[di+3] = '='
			if m == 1 {
				dst[di+2] = '='
			}
		}
	}
	return dst
}

var lens = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 31, 100, 255, 1000}

func eachN(seed int64, f func(int, []byte)) {
	r := rand.New(rand.NewSource(seed))
	for _, n := range lens {
		for k := 0; k < 4; k++ {
			src := make([]byte, n)
			r.Read(src)
			f(n, src)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	eachN(1, func(n int, src []byte) {
		out, err := b64.Decode(b64.Encode(src))
		if err != nil || !bytes.Equal(out, src) {
			t.Fatalf("n=%d %v", n, err)
		}
	})
}

func TestEncodeCanonical(t *testing.T) {
	eachN(2, func(n int, src []byte) {
		enc := b64.Encode(src)
		if string(enc) != base64.StdEncoding.EncodeToString(src) {
			t.Fatalf("n=%d %q", n, enc)
		}
	})
}

func TestTextbookMatch(t *testing.T) {
	eachN(3, func(n int, src []byte) {
		if !bytes.Equal(b64.Encode(src), textbookEncode(src)) {
			t.Fatalf("n=%d", n)
		}
	})
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"Zm9*", b64.ErrChar}, {"abc", b64.ErrLength},
		{"Zm=9", b64.ErrPadding}, {"Z===", b64.ErrPadding},
		{"Zm9=", b64.ErrUnusedBits},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		out, err := b64.Decode([]byte(c.in))
		if !errors.Is(err, c.want) || out != nil {
			t.Fatalf("%q: %v partial=%v", c.in, err, out)
		}
		seen[c.want] = true
	}
	if len(seen) != 4 {
		t.Fatal("sentinels not distinct")
	}
}

func TestReusableAfterError(t *testing.T) {
	for _, in := range [4]string{"Zm9*", "abc", "Z===", "Zm9="} {
		if out, err := b64.Decode([]byte(in)); err == nil || out != nil {
			t.Fatalf("%q accepted", in)
		}
	}
	out, err := b64.Decode(b64.Encode([]byte("still works")))
	if err != nil || string(out) != "still works" {
		t.Fatal("decoder unusable after rejection")
	}
}

func TestConcurrentEncodeDecode(t *testing.T) {
	const N = 128
	shared := b64.Encode([]byte("concurrent base64 payload"))
	want, _ := b64.Decode(shared)
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := 0
	for i := 0; i < N; i++ {
		in := []byte{byte(i), byte(i + 1), byte(i * 3)}
		wg.Add(2)
		go func() {
			defer wg.Done()
			if g, e := b64.Decode(shared); e != nil || !bytes.Equal(g, want) {
				mu.Lock()
				bad++
				mu.Unlock()
			}
		}()
		go func() {
			defer wg.Done()
			if !bytes.Equal(b64.Encode(in), textbookEncode(in)) {
				mu.Lock()
				bad++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d concurrent mismatches", bad)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
