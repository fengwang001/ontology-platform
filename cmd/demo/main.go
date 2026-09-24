package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"ontology/rle"
)

func report(name string, ok bool) bool {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
	return ok
}

func main() {
	passed := 0
	ok := func(b bool) {
		if b {
			passed++
		}
	}

	samples := map[string]string{
		"aaab": "3ab", "a1": `a\1`, "111": `3\1`, `\\\`: `3\\`,
		"aaaaaaaaaaaa": "12a", "": "",
	}
	formatOK := true
	for in, want := range samples {
		formatOK = formatOK && rle.Encode(in) == want
	}
	if report("格式样例", formatOK) {
		ok(true)
	}

	round := true
	for _, s := range []string{"a1", `\\\`, "héllo世界", "é", "e\u0301", strings.Repeat("z", 5000)} {
		out, err := rle.Decode(rle.Encode(s))
		round = round && err == nil && out == s
	}
	if report("往返", round) {
		ok(true)
	}

	type reject struct {
		in   string
		want error
		off  int
	}
	cases := []reject{
		{"1a", rle.ErrCountOne, 0}, {"0a", rle.ErrCountZero, 0},
		{"01a", rle.ErrLeadingZero, 0}, {"2a3a", rle.ErrAdjacentSame, 2},
		{`\a`, rle.ErrBadEscape, 0}, {"a\\", rle.ErrTrailingBackslash, 1},
		{"12", rle.ErrMissingSymbol, 0},
		{"a\xffb", rle.ErrInvalidUTF8, 1},
	}
	rejectOK := true
	for _, c := range cases {
		_, err := rle.Decode(c.in)
		var de *rle.DecodeError
		rejectOK = rejectOK && errors.As(err, &de) && de.Offset == c.off && errors.Is(err, c.want)
	}
	if report("拒绝清单及偏移", rejectOK) {
		ok(true)
	}

	_, bigErr := rle.Decode("99999999999999999999a")
	_, limErr := rle.Decode("99999999999999999999a", rle.WithOutputLimit(100))
	largeOK := errors.Is(bigErr, rle.ErrCountTooLarge) && errors.Is(limErr, rle.ErrCountTooLarge)
	_, smallLim := rle.Decode("100a", rle.WithOutputLimit(10))
	largeOK = largeOK && errors.Is(smallLim, rle.ErrOutputLimit)
	if report("大次数处理", largeOK) {
		ok(true)
	}

	combOK := rle.Encode("é") != rle.Encode("e\u0301")
	if dec, err := rle.Decode(rle.Encode("e\u0301")); err == nil {
		combOK = combOK && dec == "e\u0301"
	}
	if report("组合字符不合并", combOK) {
		ok(true)
	}

	splitOK := true
	texts := []string{"2a3a", "abc", "12a3b", `2\1c`, "é世界", "2a", "a"}
	for _, t := range texts {
		var ref bytes.Buffer
		refDec := rle.NewDecoder(&ref)
		_, refErr := refDec.Write([]byte(t))
		if refErr == nil {
			refErr = refDec.Close()
		}
		for cut := 0; cut <= len(t); cut++ {
			var got bytes.Buffer
			dec := rle.NewDecoder(&got)
			var err error
			if cut > 0 {
				_, err = dec.Write([]byte(t[:cut]))
			}
			if err == nil && cut < len(t) {
				_, err = dec.Write([]byte(t[cut:]))
			}
			if err == nil {
				err = dec.Close()
			}
			var e1, e2 *rle.DecodeError
			errors.As(err, &e1)
			errors.As(refErr, &e2)
			sameErr := (e1 == nil && e2 == nil) || e1 != nil && e2 != nil &&
				e1.Offset == e2.Offset && errors.Is(e1, e2.Err)
			if !sameErr || got.String() != ref.String() {
				splitOK = false
			}
		}
	}
	if report("所有切分点一致", splitOK) {
		ok(true)
	}

	enc := rle.Encode(strings.Repeat("a", 1024*1024))
	var sink bytes.Buffer
	dec := rle.NewDecoder(&sink)
	counterOK := true
	for i := 0; i < len(enc); i++ {
		if _, err := dec.Write([]byte(enc[i : i+1])); err != nil {
			counterOK = false
		}
	}
	counterOK = counterOK && dec.Close() == nil && dec.BytesChecked() == uint64(len(enc))
	if report("计数器", counterOK) {
		ok(true)
	}

	fmt.Printf("总计 %d/7\n", passed)
}
