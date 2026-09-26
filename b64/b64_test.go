package b64_test

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/b64"
)

const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// naiveRef 是手写教科书参照：逐 3 字节取 6 位、查字母表。
func naiveRef(src []byte) string {
	var out []byte
	for i := 0; i < len(src); i += 3 {
		var b [3]byte
		n := copy(b[:], src[i:])
		v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		cs := []byte{alpha[v>>18&63], alpha[v>>12&63], '=', '='}
		if n > 1 {
			cs[2] = alpha[v>>6&63]
		}
		if n > 2 {
			cs[3] = alpha[v&63]
		}
		out = append(out, cs...)
	}
	return string(out)
}

func randBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	lens := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	for i := 0; i < 60; i++ {
		lens = append(lens, r.Intn(513))
	}
	for _, n := range lens {
		b := randBytes(r, n)
		d, err := b64.Decode(b64.Encode(b))
		if err != nil || !bytes.Equal(d, b) {
			t.Fatalf("n=%d: 往返不一致, err=%v", n, err)
		}
	}
}

func TestCanonicalPadding(t *testing.T) {
	vecs := map[string]string{"": "", "f": "Zg==", "fo": "Zm8=", "foo": "Zm9v",
		"foob": "Zm9vYg==", "fooba": "Zm9vYmE=", "foobar": "Zm9vYmFy", "foobarb": "Zm9vYmFyYg=="}
	for in, want := range vecs {
		if got := string(b64.Encode([]byte(in))); got != want {
			t.Errorf("Encode(%q)=%q, 应为 %q", in, got, want)
		}
	}
	r := rand.New(rand.NewSource(2))
	for n := 1; n < 300; n++ {
		e := b64.Encode(randBytes(r, n))
		pad := len(e) - len(strings.TrimRight(string(e), "="))
		if pad != [3]int{0, 2, 1}[n%3] {
			t.Fatalf("n=%d: 填充 %d 个, 应为 %d", n, pad, [3]int{0, 2, 1}[n%3])
		}
		if s := strings.Trim(string(e[:len(e)-pad]), alpha); s != "" {
			t.Fatalf("n=%d: 输出含字母表外字符 %q", n, s)
		}
	}
}

func TestMatchesNaiveReference(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, s := range []string{"", "f", "fo", "foo", "foob", "fooba", "foobar", "foobarb"} {
		if got := string(b64.Encode([]byte(s))); got != naiveRef([]byte(s)) {
			t.Errorf("Encode(%q)=%q, 参照 %q", s, got, naiveRef([]byte(s)))
		}
	}
	for i := 0; i < 100; i++ {
		b := randBytes(r, r.Intn(256))
		if got := string(b64.Encode(b)); got != naiveRef(b) {
			t.Fatalf("随机输入不一致: got %q, 参照 %q", got, naiveRef(b))
		}
	}
}

func TestFaultInjection(t *testing.T) {
	cases := []struct {
		name, in string
		want     error
	}{
		{"非法字符", "Zm$v", b64.ErrInvalidChar},
		{"长度非4倍数", "TWE", b64.ErrLength},
		{"=出现在中间", "Zm=v", b64.ErrPadding},
		{"=超过2个", "Z===", b64.ErrPadding},
		{"末尾未用位非零(1个=)", "Zm9=", b64.ErrTrailingBits},
		{"末尾未用位非零(2个=)", "Zh==", b64.ErrTrailingBits},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		r, err := b64.Decode([]byte(c.in))
		if !errors.Is(err, c.want) || r != nil {
			t.Errorf("%s: Decode(%q)=(%v,%v), 应为 (nil,%v)", c.name, c.in, r, err, c.want)
		}
		seen[c.want] = true
		if d, err := b64.Decode([]byte("TWFu")); err != nil || string(d) != "Man" {
			t.Errorf("%s 被拒后无法继续正常使用", c.name)
		}
	}
	if len(seen) != 4 {
		t.Errorf("哨兵错误应互不相同, 实际 %d 种", len(seen))
	}
}

func TestConcurrent(t *testing.T) {
	const n = 64
	r := rand.New(rand.NewSource(4))
	shared := b64.Encode(randBytes(r, 1024))
	inputs := make([][]byte, n)
	for i := range inputs {
		inputs[i] = randBytes(r, 1+i)
	}
	decs, encs := make([][]byte, n), make([][]byte, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); decs[i], _ = b64.Decode(shared) }(i)
		go func(i int) { defer wg.Done(); encs[i] = b64.Encode(inputs[i]) }(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if !bytes.Equal(decs[i], decs[0]) || !bytes.Equal(encs[i], b64.Encode(inputs[i])) {
			t.Fatalf("并发结果不一致 i=%d", i)
		}
	}
}

func TestAPISelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
