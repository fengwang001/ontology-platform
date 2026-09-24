// Command demo prints OK/FAIL checks for the rle package.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/rle"
	"ontology/runs"
)

var checks, fails int

func check(name string, ok bool) {
	checks++
	verdict := "OK  "
	if !ok {
		fails++
		verdict = "FAIL"
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func samples() bool {
	cases := []struct{ in, want string }{
		{"aaab", "3ab"}, {"a1", `a\1`}, {"111", `3\1`}, {`\\\`, `3\\`},
		{"aaaaaaaaaaaa", "12a"}, {"", ""},
	}
	for _, c := range cases {
		if rle.Encode(c.in) != c.want {
			return false
		}
	}
	return true
}

func bigCount() bool {
	if _, err := rle.Decode("99999999999999999999a"); !errors.Is(err, rle.ErrTooLarge) {
		return false
	}
	d := rle.NewDecoder(5)
	if _, err := d.Write([]byte("10a")); !errors.Is(err, rle.ErrTooLarge) {
		return false
	}
	d = rle.NewDecoder(20)
	if _, err := d.Write([]byte("10a")); err != nil {
		return false
	}
	s, err := d.Close()
	return err == nil && s == strings.Repeat("a", 10)
}

func combining() bool {
	pre, de := "é", "e\u0301"
	if rle.Encode(pre) != pre || rle.Encode(de) != de {
		return false
	}
	got, err := rle.Decode(rle.Encode(de + de))
	return err == nil && got == de+de
}

func splits() bool {
	inputs := []string{"3ab", `a\1`, "2a3a", "10é中", `2\`, "2", "\xff", "99999999999999999999a"}
	for _, in := range inputs {
		want, wantErr := rle.Decode(in)
		for i := 0; i <= len(in); i++ {
			d := rle.NewDecoder(1 << 30)
			d.Write([]byte(in[:i]))
			d.Write([]byte(in[i:]))
			got, err := d.Close()
			if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
				return false
			}
		}
	}
	return true
}

func counter() bool {
	in := strings.Repeat(`3ab\1`, 1<<20/5)
	d := rle.NewDecoder(1 << 30)
	for i := 0; i < len(in); i++ {
		if _, err := d.Write([]byte{in[i]}); err != nil {
			return false
		}
	}
	if _, err := d.Close(); err != nil {
		return false
	}
	return d.Checked() == int64(len(in))
}

func main() {
	check("format samples", samples())
	check("round trip", roundTrip())
	check("reject classes with offsets", rejects())
	check("big count handling", bigCount())
	check("combining marks not merged", combining())
	check("all split points consistent", splits())
	check("byte check counter", counter())
	fmt.Printf("total: %d checks, %d failed\n", checks, fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func roundTrip() bool {
	rng := rand.New(rand.NewSource(7))
	alpha := []rune(`a1\é中`)
	for i := 0; i < 300; i++ {
		var b []rune
		for n := rng.Intn(30); n >= 0; n-- {
			r := alpha[rng.Intn(len(alpha))]
			for k := rng.Intn(5) + 1; k > 0; k-- {
				b = append(b, r)
			}
		}
		s := string(b)
		if got, err := rle.Decode(rle.Encode(s)); err != nil || got != s {
			return false
		}
	}
	return true
}

func rejects() bool {
	cases := []struct {
		in   string
		kind error
		off  int64
	}{
		{"1a", runs.ErrExplicitOne, 0}, {"0a", runs.ErrZeroCount, 0},
		{"01a", runs.ErrLeadingZero, 0}, {"2a3a", rle.ErrAdjacentSame, 2},
		{`\q`, rle.ErrBadEscape, 1}, {`\`, rle.ErrLoneBackslash, 0},
		{"2", rle.ErrMissingSymbol, 0}, {"\xff", rle.ErrInvalidUTF8, 0},
		{"99999999999999999999a", rle.ErrTooLarge, 0},
	}
	for _, c := range cases {
		_, err := rle.Decode(c.in)
		var de *rle.Error
		if !errors.As(err, &de) || !errors.Is(err, c.kind) || de.Off != c.off {
			return false
		}
	}
	return true
}
