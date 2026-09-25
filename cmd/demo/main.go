// Command demo exercises the rle codec and prints one OK/FAIL line
// per check, then a summary. It always exits 0.
package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/rle"
	"ontology/runs"
)

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", status, name)
}

// errKey extracts the comparable identity of a decode error.
func errKey(err error) (error, int) {
	var de *rle.DecodeError
	if errors.As(err, &de) {
		return de.Kind, de.Off
	}
	return nil, -1
}

func main() {
	samples := []struct{ s, enc string }{
		{"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`}, {`\\\`, `3\\`},
		{strings.Repeat("a", 12), "12a"}, {"", ""},
	}
	ok := true
	for _, c := range samples {
		back, err := rle.Decode(c.enc)
		ok = ok && rle.Encode(c.s) == c.enc && err == nil && back == c.s
	}
	check("format samples", ok)

	rt := []string{"a", "aaabb", "111222", `a\b\\`, "éé漢字", "éé", strings.Repeat("x", 5000)}
	ok = true
	for _, s := range rt {
		back, err := rle.Decode(rle.Encode(s))
		ok = ok && err == nil && back == s
	}
	check("roundtrip assorted", ok)

	rejects := []struct {
		in   string
		kind error
		off  int
	}{
		{"1a", runs.ErrExplicitOne, 0}, {"0a", runs.ErrZeroCount, 0},
		{"01a", runs.ErrLeadingZero, 0}, {"2a3a", rle.ErrAdjacentSame, 2},
		{`\a`, rle.ErrBadEscape, 1}, {`a\`, rle.ErrDanglingEscape, 1},
		{"2", rle.ErrMissingSymbol, 0}, {"\xff", runs.ErrInvalidUTF8, 0},
	}
	ok = true
	for _, r := range rejects {
		_, err := rle.Decode(r.in)
		kind, off := errKey(err)
		ok = ok && kind == r.kind && off == r.off
	}
	check("reject list kinds+offsets", ok)

	_, errBig := rle.Decode("99999999999999999999a")
	ok = errors.Is(errBig, rle.ErrOutputLimit)
	d := rle.NewDecoder(20)
	d.Write([]byte("10a"))
	s, err := d.Close()
	ok = ok && err == nil && s == strings.Repeat("a", 10)
	d = rle.NewDecoder(5)
	d.Write([]byte("10a"))
	_, err = d.Close()
	ok = ok && errors.Is(err, rle.ErrOutputLimit)
	check("big count: no overflow, limit enforced", ok)

	comb := "éé"
	back, err := rle.Decode(rle.Encode(comb))
	ok = rle.Encode("éé") == "2é" && rle.Encode(comb) == comb && err == nil && back == comb
	check("combining marks not merged", ok)

	ok = true
	for _, t := range []string{"2a3a", "12aé\\1", `a\`, "1a", "\xff", "10é2b", ""} {
		want, werr := rle.Decode(t)
		wk, wo := errKey(werr)
		for k := 0; k <= len(t); k++ {
			d := rle.NewDecoder(1 << 30)
			d.Write([]byte(t[:k]))
			d.Write([]byte(t[k:]))
			got, err := d.Close()
			gk, go_ := errKey(err)
			ok = ok && got == want && gk == wk && go_ == wo
		}
	}
	check("all split points identical (incl 2a|3a)", ok)

	data := strings.Repeat(`3a3bé\1`, 1<<17)
	want, _ := rle.Decode(data)
	d = rle.NewDecoder(1 << 30)
	for i := 0; i < len(data); i++ {
		d.Write([]byte(data[i : i+1]))
	}
	got, err := d.Close()
	check("1MB bytewise == oneshot (counter pinned in tests)", err == nil && got == want)

	fmt.Printf("total: %d failed\n", fails)
}
