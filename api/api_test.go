package api_test

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

// naiveEncode 是手写教科书参照：按码点范围逐段取位，拼出 110/1110/11110 前缀。
func naiveEncode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | (byte(r) & 0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | (byte(r>>6) & 0x3F), 0x80 | (byte(r) & 0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | (byte(r>>12) & 0x3F),
			0x80 | (byte(r>>6) & 0x3F), 0x80 | (byte(r) & 0x3F)}
	}
}

// 测试向量：边界点 + 循环生成的确定伪随机码点（LCG，跳过代理项）。
func sampleRunes() []rune {
	rs := []rune{0x00, 0x41, 0x7F, 0x80, 0xA2, 0x7FF, 0x800, 0x20AC, 0xD7FF, 0xE000, 0xFFFF, 0x10000, 0x1F600, 0x10FFFF}
	seed := uint32(12345)
	for i := 0; i < 500; i++ {
		seed = seed*1664525 + 1013904223
		if r := rune(seed % 0x110000); r < 0xD800 || r > 0xDFFF {
			rs = append(rs, r)
		}
	}
	return rs
}

func TestEncodeMatchesNaive(t *testing.T) {
	for _, r := range sampleRunes() {
		got, err := enc.EncodeRune(r)
		if err != nil {
			t.Fatalf("EncodeRune(U+%04X): %v", r, err)
		}
		if want := naiveEncode(r); !bytes.Equal(got, want) {
			t.Fatalf("U+%04X: got % X, want % X", r, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, r := range sampleRunes() {
		b, _ := enc.EncodeRune(r)
		got, n, err := enc.DecodeRune(b)
		if err != nil || got != r || n != len(b) {
			t.Fatalf("U+%04X: got U+%04X n=%d err=%v", r, got, n, err)
		}
	}
	a := api.New()
	s := string(sampleRunes())
	b, err := a.EncodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := a.DecodeString(b); err != nil || back != s {
		t.Fatalf("api roundtrip mismatch: %v", err)
	}
}

func TestDecodeVectors(t *testing.T) {
	cases := []struct {
		in   []byte
		want rune
		n    int
		err  error
	}{
		{[]byte{0x41}, 0x0041, 1, nil},
		{[]byte{0xC2, 0xA2}, 0x00A2, 2, nil},
		{[]byte{0xC0, 0xAF}, 0, 0, enc.ErrOverlong},
		{[]byte{0xE2, 0x82, 0xAC}, 0x20AC, 3, nil},
		{[]byte{0xED, 0xA0, 0x80}, 0, 0, enc.ErrSurrogate},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, 0x1F600, 4, nil},
		{[]byte{0xF4, 0x8F, 0xBF, 0xBF}, 0x10FFFF, 4, nil},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, 0, 0, enc.ErrOutOfRange},
		{[]byte{0x80}, 0, 0, enc.ErrBadLead},
		{[]byte{0xFF}, 0, 0, enc.ErrBadLead},
		{[]byte{0xE2, 0x82}, 0, 0, enc.ErrTruncated},
		{[]byte{0xE2, 0x28, 0xAC}, 0, 0, enc.ErrBadContinuation},
		{nil, 0, 0, enc.ErrTruncated},
	}
	for _, c := range cases {
		r, n, err := enc.DecodeRune(c.in)
		if !errors.Is(err, c.err) || r != c.want || n != c.n {
			t.Fatalf("% X: got (%U,%d,%v), want (%U,%d,%v)", c.in, r, n, err, c.want, c.n, c.err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrent(t *testing.T) {
	a := api.New()
	var buf []byte
	for _, r := range sampleRunes() {
		b, _ := enc.EncodeRune(r)
		buf = append(buf, b...)
	}
	want, err := stream.DecodeAll(buf)
	if err != nil {
		t.Fatal(err)
	}
	const N = 32
	var wg sync.WaitGroup
	errs := make(chan string, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got, err := stream.DecodeAll(buf) // 同一段只读字节
			if err != nil || !reflect.DeepEqual(got, want) {
				errs <- "decode mismatch"
			}
			s := string(sampleRunes()[:g+1]) // 各自不同的字符串
			b, err := a.EncodeString(s)
			serial, err2 := a.EncodeString(s) // 与串行逐一编码相同
			if err != nil || err2 != nil || !bytes.Equal(b, serial) {
				errs <- "encode mismatch"
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
