package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

// checkEnc 核验第三节八个序列与编码字节。
func checkEnc() {
	type vec struct {
		in   []byte
		want rune
		err  error
	}
	vecs := []vec{
		{[]byte{0x41}, 0x0041, nil},
		{[]byte{0xC2, 0xA2}, 0x00A2, nil},
		{[]byte{0xC0, 0xAF}, 0, enc.ErrOverlong},
		{[]byte{0xE2, 0x82, 0xAC}, 0x20AC, nil},
		{[]byte{0xED, 0xA0, 0x80}, 0, enc.ErrSurrogate},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, 0x1F600, nil},
		{[]byte{0xF4, 0x8F, 0xBF, 0xBF}, 0x10FFFF, nil},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, 0, enc.ErrOutOfRange},
	}
	ok := true
	for _, v := range vecs {
		r, _, err := enc.DecodeRune(v.in)
		if !errors.Is(err, v.err) || (err == nil && r != v.want) {
			ok = false
		}
	}
	check("eight-vectors", ok)

	encOK := true
	for r, want := range map[rune][]byte{
		0x00A2:  {0xC2, 0xA2},
		0x20AC:  {0xE2, 0x82, 0xAC},
		0x1F600: {0xF0, 0x9F, 0x98, 0x80},
	} {
		got, err := enc.EncodeRune(r)
		encOK = encOK && err == nil && bytes.Equal(got, want)
	}
	check("encode-bytes", encOK)

	_, _, err := enc.DecodeRune([]byte{0xE2, 0x28, 0xAC})
	check("reject-bad-continuation", errors.Is(err, enc.ErrBadContinuation))
}

// checkStream 核验游标不变式与单趟解码。
func checkStream() {
	// 被拒后游标不变，且后续读取不受影响。
	r := stream.NewReader([]byte{0x41, 0xC0, 0xAF, 0x42})
	rn, err := r.Next()
	ok := err == nil && rn == 'A' && r.Pos() == 1
	_, err = r.Next()
	ok = ok && errors.Is(err, enc.ErrOverlong) && r.Pos() == 1
	r.Reset([]byte{0x41, 0x42})
	rn, err = r.Next()
	ok = ok && err == nil && rn == 'A' && r.Pos() == 1
	check("cursor-stable-after-reject", ok)

	// 大 m 单趟解码：消耗字节数恰好等于缓冲长（每字节只读一次，零回看由测试钉住）。
	var buf []byte
	for i := 0; i < 10000; i++ {
		b, _ := enc.EncodeRune(rune(0x41 + i%0x3000))
		buf = append(buf, b...)
	}
	rs, err := stream.DecodeAll(buf)
	check("single-pass-m10000", err == nil && len(rs) == 10000)
}

// checkAPI 核验自检、字符串往返与并发一致性。
func checkAPI() {
	a := api.New()
	check("selfcheck", a.SelfCheck() == nil)

	b, err := a.EncodeString("A¢€😀")
	s, err2 := a.DecodeString(b)
	check("string-roundtrip", err == nil && err2 == nil && s == "A¢€😀")

	// 并发：16 goroutine 对同一段字节 DecodeAll，另 16 个各自 EncodeString。
	var buf []byte
	for i := 0; i < 2000; i++ {
		e, _ := enc.EncodeRune(rune(0x41 + i%0x3000))
		buf = append(buf, e...)
	}
	want, _ := stream.DecodeAll(buf)
	var wg sync.WaitGroup
	ok := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rs, err := stream.DecodeAll(buf)
			if err != nil || !reflect.DeepEqual(rs, want) {
				ok = false
			}
			e, err := a.EncodeString(string(rune('a'+g)) + "€😀")
			d, err2 := a.DecodeString(e)
			if err != nil || err2 != nil || d != string(rune('a'+g))+"€😀" {
				ok = false
			}
		}(g)
	}
	wg.Wait()
	check("concurrent-consistent", ok)
}

func main() {
	checkEnc()
	checkStream()
	checkAPI()
	if failed {
		os.Exit(1)
	}
}
