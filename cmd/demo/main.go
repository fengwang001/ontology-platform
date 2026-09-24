// Command demo exercises the rle codec end to end.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ontology/rle"
)

var failures int

func report(name string, ok bool) {
	if !ok {
		failures++
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func decodeChunks(in string, cuts ...int) (string, error) {
	var buf bytes.Buffer
	d := rle.NewDecoder(&buf, rle.DefaultLimit)
	prev := 0
	for _, c := range cuts {
		d.Write([]byte(in[prev:c]))
		prev = c
	}
	d.Write([]byte(in[prev:]))
	return buf.String(), d.Close()
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	formatOK := rle.Encode("aaab") == "3ab" && rle.Encode("a1") == `a\1` &&
		rle.Encode("111") == `3\1` && rle.Encode(`\\\`) == `3\\` &&
		rle.Encode(strings.Repeat("a", 12)) == "12a" && rle.Encode("") == ""
	report("format samples", formatOK)

	roundOK := true
	for _, s := range []string{"", "a1\\é日", strings.Repeat("z", 999), "e\u0301é", `\\1`} {
		dec, err := rle.Decode(rle.Encode(s))
		if err != nil || dec != s {
			roundOK = false
		}
	}
	report("roundtrip", roundOK)

	rejectCases := []struct {
		in   string
		kind error
		off  int
	}{
		{"1a", rle.ErrCountOne, 0},
		{"0a", rle.ErrCountZero, 0},
		{"01a", rle.ErrLeadingZero, 0},
		{"2a3a", rle.ErrAdjacentSame, 2},
		{`\a`, rle.ErrBadEscape, 0},
		{"a\\", rle.ErrTrailingEscape, 1},
		{"3", rle.ErrMissingSymbol, 1},
		{"\xff", rle.ErrInvalidUTF8, 0},
	}
	rejectOK := true
	for _, c := range rejectCases {
		_, err := rle.Decode(c.in)
		var de *rle.Error
		if !errors.As(err, &de) || !errors.Is(err, c.kind) || de.Off != c.off {
			rejectOK = false
		}
	}
	report("reject list+offsets", rejectOK)

	_, bigErr := rle.Decode("99999999999999999999a")
	bigOut, err := rle.DecodeLimit("1000000b", 1<<20)
	bigOK := errors.Is(bigErr, rle.ErrOutputLimit) && err == nil &&
		bigOut == strings.Repeat("b", 1000000) && rle.Encode(bigOut) == "1000000b"
	report("big count", bigOK)

	combOK := rle.Encode("éé") == "2é" && rle.Encode("e\u0301e\u0301") == "e\u0301e\u0301"
	report("no unicode merge", combOK)

	splitOK := true
	for _, in := range []string{"2a3a", `3\1x`, "12é3\\", "abc"} {
		wantS, wantE := decodeChunks(in)
		for cut := 0; cut <= len(in); cut++ {
			gotS, gotE := decodeChunks(in, cut)
			if gotS != wantS || errStr(gotE) != errStr(wantE) {
				splitOK = false
			}
		}
	}
	report("split points (2a|3a)", splitOK)

	enc := rle.Encode(strings.Repeat("ab1\\é", 150000))
	before := rle.CheckedBytes()
	d := rle.NewDecoder(io.Discard, rle.DefaultLimit)
	for i := 0; i < len(enc); i++ {
		d.Write([]byte(enc[i : i+1]))
	}
	cntOK := d.Close() == nil && rle.CheckedBytes()-before == int64(len(enc))
	report("byte counter", cntOK)

	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
