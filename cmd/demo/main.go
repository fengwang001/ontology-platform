package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/rle"
)

type check struct {
	name string
	ok   bool
}

func decErr(t string) (int, error) {
	_, err := rle.Decode(t)
	var de *rle.DecodeError
	if errors.As(err, &de) {
		return de.Offset, de.Err
	}
	return -1, err
}

func expectErr(t string, want error, wantOff int) bool {
	off, got := decErr(t)
	return errors.Is(got, want) && off == wantOff
}

func splitsAgree(t string) bool {
	want, werr := rle.Decode(t)
	var wde *rle.DecodeError
	errors.As(werr, &wde)
	for k := 0; k <= len(t); k++ {
		var sb strings.Builder
		d := rle.NewDecoder(&sb, rle.DefaultOutputLimit)
		var e1, e2 error
		if k > 0 {
			_, e1 = d.Write([]byte(t[:k]))
		}
		if e1 == nil && k < len(t) {
			_, e2 = d.Write([]byte(t[k:]))
		}
		cerr := d.Close()
		err := e1
		if err == nil {
			err = e2
		}
		if err == nil {
			err = cerr
		}
		if (err == nil) != (werr == nil) || err == nil && sb.String() != want {
			return false
		}
		if werr != nil {
			var cde *rle.DecodeError
			if !errors.As(err, &cde) || !errors.Is(cde.Err, wde.Err) || cde.Offset != wde.Offset {
				return false
			}
		}
	}
	return true
}

func main() {
	samples := map[string]string{
		"aaab":           "3ab",
		"a1":             "a\\1",
		"111":            "3\\1",
		"\\\\\\":         "3\\\\",
		"aaaaaaaaaaaa":   "12a",
		"":               "",
		"é":              "é",
		"e\u0301e\u0301": "e\u0301e\u0301",
	}
	sampleOK := true
	for in, want := range samples {
		if rle.Encode(in) != want {
			sampleOK = false
		}
	}

	rtSrc := "a1\\\\世éé\u0000aaaaaaaaaaaaaaaa"
	rtOut, rtErr := rle.Decode(rle.Encode(rtSrc))
	rt := rtErr == nil && rtOut == rtSrc

	combining := rle.Encode("é") != rle.Encode("e\u0301")

	huge := expectErr("99999999999999999999a", rle.ErrCountTooLarge, 0)

	counterOK := func() bool {
		text := strings.Repeat("a", 1<<20)
		enc := "1048576a"
		var sb strings.Builder
		d := rle.NewDecoder(&sb, rle.DefaultOutputLimit)
		buf := []byte(enc)
		for i := 0; i < len(buf); i++ {
			if _, err := d.Write(buf[i : i+1]); err != nil {
				return false
			}
		}
		if d.Close() != nil {
			return false
		}
		return d.Checked() == len(enc) && sb.Len() == len(text)
	}()

	checks := []check{
		{"format samples", sampleOK},
		{"roundtrip", rt},
		{"count 1 @1", expectErr("1a", rle.ErrCountOne, 1)},
		{"count 0 @0", expectErr("0a", rle.ErrCountZero, 0)},
		{"leading zero @0", expectErr("01a", rle.ErrLeadingZero, 0)},
		{"adjacent same @3", expectErr("2a3a", rle.ErrAdjacentSymbol, 3)},
		{"bad escape @0", expectErr("\\x", rle.ErrBadEscape, 0)},
		{"trailing slash @0", expectErr("\\", rle.ErrTrailingSlash, 0)},
		{"trailing count @1", expectErr("a2", rle.ErrTrailingCount, 1)},
		{"invalid utf8 @0", expectErr("\xff", rle.ErrInvalidUTF8, 0)},
		{"huge count rejected", huge},
		{"combining not merged", combining},
		{"splits agree (valid)", splitsAgree("12a3b2\\1")},
		{"splits agree (2a|3a)", splitsAgree("2a3a")},
		{"checked == input bytes", counterOK},
	}

	pass := 0
	for _, c := range checks {
		mark := "OK"
		if !c.ok {
			mark = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s  %s\n", mark, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
