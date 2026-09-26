package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

var fails int

func ok(name string, cond bool) {
	if !cond {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 第三节八个序列：码点或拒绝原因。
	seqs := []struct {
		b    []byte
		want string
	}{
		{[]byte{0x41}, "U+0041"}, {[]byte{0xC2, 0xA2}, "U+00A2"},
		{[]byte{0xC0, 0xAF}, "ErrOverlong"}, {[]byte{0xE2, 0x82, 0xAC}, "U+20AC"},
		{[]byte{0xED, 0xA0, 0x80}, "ErrSurrogate"}, {[]byte{0xF0, 0x9F, 0x98, 0x80}, "U+1F600"},
		{[]byte{0xF4, 0x8F, 0xBF, 0xBF}, "U+10FFFF"}, {[]byte{0xF4, 0x90, 0x80, 0x80}, "ErrOutOfRange"},
	}
	got, want := "", ""
	for _, s := range seqs {
		r, _, err := enc.DecodeRune(s.b)
		switch {
		case errors.Is(err, enc.ErrOverlong):
			got += "ErrOverlong "
		case errors.Is(err, enc.ErrSurrogate):
			got += "ErrSurrogate "
		case errors.Is(err, enc.ErrOutOfRange):
			got += "ErrOutOfRange "
		case err == nil:
			got += fmt.Sprintf("U+%04X ", r)
		default:
			got += err.Error() + " "
		}
		want += s.want + " "
	}
	ok("eight-sequences", got == want)

	// U+00A2 / U+20AC / U+1F600 的编码字节。
	encOK := true
	for r, w := range map[rune][]byte{0xA2: {0xC2, 0xA2}, 0x20AC: {0xE2, 0x82, 0xAC}, 0x1F600: {0xF0, 0x9F, 0x98, 0x80}} {
		b, err := enc.EncodeRune(r)
		encOK = encOK && err == nil && string(b) == string(w)
	}
	ok("encode-U+00A2/20AC/1F600", encOK)

	// 往返一致：1..4 字节代表码点 EncodeRune 后 DecodeAll 解回。
	runes := []rune{0x41, 0xA2, 0x7FF, 0x800, 0x20AC, 0xFFFD, 0x10000, 0x1F600, 0x10FFFF}
	var buf []byte
	rt := true
	for _, r := range runes {
		b, err := enc.EncodeRune(r)
		rt = rt && err == nil
		buf = append(buf, b...)
	}
	back, err := stream.DecodeAll(buf)
	rt = rt && err == nil && string(back) == string(runes)
	ok("roundtrip", rt)

	// 四类拒绝（哨兵错误可判定）。
	rej := func(b []byte, s error) bool {
		_, _, err := enc.DecodeRune(b)
		return errors.Is(err, s)
	}
	ok("reject-overlong/surrogate/range/badcont",
		rej([]byte{0xC0, 0xAF}, enc.ErrOverlong) &&
			rej([]byte{0xED, 0xA0, 0x80}, enc.ErrSurrogate) &&
			rej([]byte{0xF4, 0x90, 0x80, 0x80}, enc.ErrOutOfRange) &&
			rej([]byte{0xE2, 0x28, 0xAC}, enc.ErrBadContinuation))

	// 被拒后游标不变，读取器不损坏，Reset 后仍可正常读。
	rd := stream.NewReader([]byte{0xC0, 0xAF, 0x41})
	p0 := rd.Pos()
	_, err1 := rd.Next()
	_, err2 := rd.Next()
	pinned := err1 != nil && err2 != nil && rd.Pos() == p0
	rd.Reset([]byte{0x41})
	r3, err3 := rd.Next()
	ok("cursor-pinned-after-reject", pinned && err3 == nil && r3 == 0x41 && rd.Pos() == 1)

	// 大 m 单趟解码（零回看由 stream 包内测试断言，这里验证大缓冲正确解出）。
	var big []byte
	var wantRunes []rune
	for i := 0; i < 10000; i++ {
		r := runes[i%len(runes)]
		b, _ := enc.EncodeRune(r)
		big = append(big, b...)
		wantRunes = append(wantRunes, r)
	}
	all, err := stream.DecodeAll(big)
	ok("single-pass-large-m", err == nil && string(all) == string(wantRunes))

	// api 自检（四条不变量）与并发一致性。
	c := api.New()
	ok("selfcheck", c.SelfCheck() == nil)
	const N = 16
	var wg sync.WaitGroup
	decOK := make([]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs, err := stream.DecodeAll(big)
			decOK[g] = err == nil && string(rs) == string(wantRunes)
		}()
	}
	encOK2 := make([]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			s := string(runes[:g%len(runes)+1]) + string(rune(0x41+g))
			b, err := c.EncodeString(s)
			back, err2 := c.DecodeString(b)
			encOK2[g] = err == nil && err2 == nil && back == s
		}(g)
	}
	wg.Wait()
	conc := true
	for g := 0; g < N; g++ {
		conc = conc && decOK[g] && encOK2[g]
	}
	ok("concurrent-consistent", conc)

	if fails > 0 {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: OK")
}
