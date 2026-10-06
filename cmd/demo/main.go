// Command demo exercises the strict JSON string codec.
package main

import (
	"errors"
	"fmt"

	"ontology/jstr"
)

var pass, total int

func check(name string, ok bool) {
	total++
	if ok {
		pass++
	}
	fmt.Printf("%-4s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func seKind(lit string) (int, int) {
	_, err := jstr.Decode([]byte(lit))
	var se *jstr.SyntaxError
	if errors.As(err, &se) {
		return se.Kind, se.Offset
	}
	return -1, -1
}

func main() {
	type ec struct {
		lit  string
		k, o int
	}
	errs := []ec{
		{"\"a\x00b\"", jstr.KindControl, 2},
		{`"\x"`, jstr.KindEscape, 1},
		{`"\u12"`, jstr.KindUnicode, 1},
		{`"abc`, jstr.KindUnterminated, 4},
		{`"a"x`, jstr.KindTrailing, 3},
	}
	okErrs := true
	for _, c := range errs {
		k, o := seKind(c.lit)
		okErrs = okErrs && k == c.k && o == c.o
	}
	check("five error kinds + offsets", okErrs)

	sur := []string{`"\uD83D"`, `"\uDE00"`, `"\uD83Dx"`, `"\uD83DA"`, `"\uDE00\uD83D"`}
	okSur := true
	for _, s := range sur {
		_, e := jstr.Decode([]byte(s))
		okSur = okSur && e != nil
	}
	grin, gerr := jstr.Decode([]byte(`"😀"`))
	check("surrogate samples (grin=U+1F600, others rejected)", okSur && gerr == nil && grin == "😀")

	_, dErr := jstr.Decode([]byte("\"a\xffb\""))
	_, eErr := jstr.Encode("a\xffb")
	check("invalid UTF-8 rejected both directions", dErr != nil && errors.Is(eErr, jstr.ErrInvalidUTF8))

	enc, _ := jstr.Encode("/\x7f\u2028")
	check("minimal escaping: / DEL U+2028 verbatim", string(enc) == "\"/\x7f\u2028\"")

	s := "a\"\\b\x00\t😀/\x7f"
	rt, rerr := jstr.Encode(s)
	back, berr := jstr.Decode(rt)
	check("round trip Decode(Encode(s))==s", rerr == nil && berr == nil && back == s)

	lit := []byte(`"\uD83D\uDE00ab\u00e9"`)
	base, _ := jstr.Decode(lit)
	okSplit := true
	for cut := 0; cut < len(lit); cut++ {
		d := jstr.NewDecoder()
		for i := 0; i < len(lit); i += cut + 1 {
			j := i + cut + 1
			if j > len(lit) {
				j = len(lit)
			}
			if _, err := d.Write(lit[i:j]); err != nil {
				okSplit = false
			}
		}
		g, err := d.Close()
		okSplit = okSplit && err == nil && g == base
	}
	check("all split points identical", okSplit)

	var sb []byte
	sb = append(sb, '"')
	for i := 0; i < 50000; i++ {
		sb = append(sb, []byte(`\uD83D\uDE00`)...)
	}
	sb = append(sb, '"')
	d := jstr.NewDecoder()
	_, werr := d.Write(sb)
	_, cerr := d.Close()
	check("inspection counter <= 2*bytes", werr == nil && cerr == nil && d.Checks() <= 2*len(sb))

	fmt.Printf("TOTAL %d/%d\n", pass, total)
}
