package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

// refEncode 手写教科书参照：按码点范围逐段取位、拼 110/1110/11110 前缀。
func refEncode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}

// 不变量 1：往返一致。边界表 + 全码点循环。
func TestRoundtrip(t *testing.T) {
	table := []rune{0x00, 0x41, 0x7F, 0x80, 0xA2, 0x7FF, 0x800, 0x20AC, 0xD7FF, 0xE000, 0xFFFD, 0xFFFF, 0x10000, 0x1F600, 0x10FFFF}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r < 0xD800 || r > 0xDFFF {
			table = append(table, r)
		}
	}
	for _, r := range table {
		b, err := enc.EncodeRune(r)
		if err != nil {
			t.Fatalf("EncodeRune(U+%04X): %v", r, err)
		}
		if got, n, err := enc.DecodeRune(b); err != nil || got != r || n != len(b) {
			t.Fatalf("roundtrip U+%04X: got U+%04X n=%d err=%v", r, got, n, err)
		}
	}
}

// 不变量 3：EncodeRune 与朴素参照字节级一致（全码点）。
func TestReferenceMatch(t *testing.T) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		b, err := enc.EncodeRune(r)
		if err != nil || string(b) != string(refEncode(r)) {
			t.Fatalf("U+%04X: % X vs ref % X (err=%v)", r, b, refEncode(r), err)
		}
	}
}

// 不变量 2：六类非法输入各被互不相同的哨兵错误拒绝。
func TestRejectTable(t *testing.T) {
	sentinels := []error{enc.ErrInvalidLead, enc.ErrOverlong, enc.ErrSurrogate, enc.ErrOutOfRange, enc.ErrTruncated, enc.ErrBadContinuation}
	seen := map[error]bool{} // 互不相同
	for _, e := range sentinels {
		if seen[e] {
			t.Fatal("sentinels not distinct")
		}
		seen[e] = true
	}
	cases := []struct {
		name string
		b    []byte
		err  error
	}{
		{"lead-continuation", []byte{0x80}, enc.ErrInvalidLead}, {"lead-f8", []byte{0xF8}, enc.ErrInvalidLead},
		{"lead-ff", []byte{0xFF}, enc.ErrInvalidLead}, {"overlong-2B", []byte{0xC0, 0xAF}, enc.ErrOverlong},
		{"overlong-3B", []byte{0xE0, 0x9F, 0x80}, enc.ErrOverlong},
		{"overlong-4B", []byte{0xF0, 0x8F, 0xBF, 0xBF}, enc.ErrOverlong},
		{"surrogate-lo", []byte{0xED, 0xA0, 0x80}, enc.ErrSurrogate},
		{"surrogate-hi", []byte{0xED, 0xBF, 0xBF}, enc.ErrSurrogate},
		{"out-of-range", []byte{0xF4, 0x90, 0x80, 0x80}, enc.ErrOutOfRange},
		{"out-of-range-max", []byte{0xF7, 0xBF, 0xBF, 0xBF}, enc.ErrOutOfRange},
		{"truncated-2B", []byte{0xC2}, enc.ErrTruncated},
		{"truncated-4B", []byte{0xF0, 0x9F, 0x98}, enc.ErrTruncated},
		{"bad-cont", []byte{0xE2, 0x28, 0xAC}, enc.ErrBadContinuation},
		{"bad-cont-tail", []byte{0xF0, 0x9F, 0x98, 0x7F}, enc.ErrBadContinuation},
	}
	for _, tc := range cases {
		if _, _, err := enc.DecodeRune(tc.b); !errors.Is(err, tc.err) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.err, err)
		}
	}
	// 编码端同样拒绝代理项与越界。
	for _, r := range []rune{0xD800, 0xDFFF, 0x110000, -1} {
		if _, err := enc.EncodeRune(r); err == nil {
			t.Errorf("EncodeRune(U+%04X) should fail", r)
		}
	}
}

// 并发：N 路 DecodeAll 同一段只读字节结果逐位相同；N 路 EncodeString 与串行一致。
func TestConcurrent(t *testing.T) {
	const N = 32
	rng := rand.New(rand.NewSource(42))
	var buf []byte
	var want []rune
	for i := 0; i < 2000; i++ {
		r := rune(rng.Intn(0x10000))
		if r >= 0xD800 && r <= 0xDFFF {
			r = 0x41
		}
		b, _ := enc.EncodeRune(r)
		buf, want = append(buf, b...), append(want, r)
	}
	c := api.New()
	strs := make([]string, N)
	serial := make([][]byte, N)
	for i := range strs {
		strs[i] = string(want[i*10:i*10+10]) + string(rune(0x41+i))
		serial[i], _ = c.EncodeString(strs[i])
	}
	var wg sync.WaitGroup
	ok := make([]bool, 2*N)
	for g := 0; g < N; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rs, err := stream.DecodeAll(buf)
			ok[g] = err == nil && string(rs) == string(want)
		}()
		go func(g int) {
			defer wg.Done()
			b, err := c.EncodeString(strs[g])
			ok[N+g] = err == nil && string(b) == string(serial[g])
		}(g)
	}
	wg.Wait()
	for g := range ok {
		if !ok[g] {
			t.Fatalf("worker %d failed", g)
		}
	}
}

// SelfCheck 自检四条不变量。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
