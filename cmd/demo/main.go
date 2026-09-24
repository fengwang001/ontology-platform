package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"ontology/rle"
)

var pass, fail int

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK  " + name)
	} else {
		fail++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	samples := map[string]string{
		"aaab":                  "3ab",
		"a1":                    "a\\1",
		"111":                   "3\\1",
		"\\\\\\":                 "3\\\\",
		"aaaaaaaaaaaa":          "12a",
		"":                      "",
		"a0123456789b":          "a\\0\\1\\2\\3\\4\\5\\6\\7\\8\\9b",
		"ééé中文":                  "3é中",
	}
	ok := true
	for in, want := range samples {
		got, err := rle.Decode(rle.Encode(in))
		ok = ok && err == nil && got == in && rle.Encode(in) == want
	}
	check("格式样例", ok)

	rt := []string{"数字123与反斜杠\\", "éé", "éé", strings.Repeat("a", 5000), "界世😀"}
	ok = true
	for _, s := range rt {
		t := rle.Encode(s)
		d, err := rle.Decode(t)
		ok = ok && err == nil && d == s && rle.Encode(d) == t
	}
	check("往返(数字/转义/多字节/长游程)", ok)

	bad := []struct {
		in     string
		offset int
		sent   error
	}{
		{"1a", 0, rle.ErrCountOne},
		{"0a", 0, rle.ErrCountZero},
		{"01a", 0, rle.ErrLeadingZero},
		{"2a3a", 2, rle.ErrAdjacentSame},
		{"a\\x", 2, rle.ErrBadEscape},
		{"a\\", 1, rle.ErrLoneBackslash},
		{"a3", 1, rle.ErrCountNoSymbol},
		{string([]byte{0x80}), 0, rle.ErrInvalidUTF8},
	}
	ok = true
	for _, c := range bad {
		_, err := rle.Decode(c.in)
		var pe *rle.ParseError
		ok = ok && errors.As(err, &pe) && pe.Offset == c.offset && errors.Is(err, c.sent)
	}
	check("拒绝清单各类错误及偏移", ok)

	_, errBig := rle.DecodeLimit("99999999999999999999a", 1<<30)
	ok = errors.Is(errBig, rle.ErrOutputLimit)
	med, errMed := rle.DecodeLimit("18446744073709551616a", 1<<65)
	check("大次数(超限判定/不溢出)", ok && errMed == nil && len(med) == 1<<64)

	nfc := "éé"
	nfd := "éé"
	check("组合字符不合并", rle.Encode(nfc) == "2é" &&
		rle.Encode(nfd) == "2e2\u0301" && nfc != nfd)

	cases := []string{"2a3b", "a\\1\\2中", "2a3a"}
	ok = true
	for _, c := range cases {
		bulk, berr := rle.Decode(c)
		for i := 0; i <= len(c); i++ {
			var buf bytes.Buffer
			d := rle.NewDecoder(&buf)
			_, e1 := d.Write([]byte(c[:i]))
			_, e2 := d.Close()
			got := buf.String()
			same := (errors.Is(e1, berr) || errors.Is(e2, berr)) &&
				(berr != nil || got == bulk)
			if berr == nil {
				same = e1 == nil && e2 == nil && got == bulk
			}
			ok = ok && same
		}
	}
	check("所有切分点一致(含2a|3a)", ok)

	src := strings.Repeat("ab", 512*1024)
	enc := rle.Encode(src)
	var out bytes.Buffer
	d := rle.NewDecoder(&out)
	for i := 0; i < len(enc); i++ {
		if _, err := d.Write(enc[i : i+1]); err != nil {
			ok = false
		}
	}
	ok = ok && d.Close() == nil && d.BytesChecked() == int64(len(enc)) &&
		len(enc) == 1024*1024 && out.String() == src
	check("计数器==输入字节数(1MB逐字节)", ok)

	fmt.Printf("总计 %d 通过 / %d 失败\n", pass, fail)
	if fail != 0 {
		panic("demo failed")
	}
}
