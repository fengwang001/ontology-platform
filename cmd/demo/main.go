// Command demo exercises the jstr strict JSON string codec end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/jstr"
)

var fails int

func check(name string, ok bool) {
	v := "OK"
	if !ok {
		v = "FAIL"
		fails++
	}
	fmt.Printf("%-22s %s\n", name, v)
}

// errIs reports whether decoding lit fails with sentinel want at offset off.
func errIs(lit string, want error, off int) bool {
	var e *jstr.Error
	_, err := jstr.Decode([]byte(lit))
	return errors.As(err, &e) && errors.Is(err, want) && e.Off == off
}

func main() {
	check("err control char", errIs("\"a\x01\"", jstr.ErrControl, 2))
	check("err unknown escape", errIs(`"\x"`, jstr.ErrEscape, 2))
	check("err short hex", errIs(`"\u12G4"`, jstr.ErrHex, 5))
	check("err unterminated", errIs(`"abc`, jstr.ErrUnterminated, 4))
	check("err trailing", errIs(`"a" `, jstr.ErrTrailing, 3))

	s, err := jstr.Decode([]byte(`"\uD83D\uDE00"`))
	check("pair -> U+1F600", err == nil && s == "\U0001F600")
	check("lone high+low", errIs(`"\uD83D"`, jstr.ErrSurrogate, 1) && errIs(`"\uDE00"`, jstr.ErrSurrogate, 1))
	check("high then x/A", errIs(`"\uD83Dx"`, jstr.ErrSurrogate, 1) && errIs(`"\uD83DA"`, jstr.ErrSurrogate, 1))
	check("reversed pair", errIs(`"\uDE00\uD83D"`, jstr.ErrSurrogate, 1))

	_, e1 := jstr.Decode([]byte{'"', 0xFF, '"'})
	_, e2 := jstr.Encode("\xff")
	check("bad utf8 both ways", errors.Is(e1, jstr.ErrUTF8) && errors.Is(e2, jstr.ErrUTF8))

	enc, _ := jstr.Encode("/\x7f\u2028\u2029a\n")
	check("minimal escaping", string(enc) == "\"/\x7f\u2028\u2029a\\n\"")

	rt := true
	for _, s := range []string{"", `a"b\c`, "\x00\x1f\x7f", "hi 世界 \U0001F600", "\u2028\u2029"} {
		b, err := jstr.Encode(s)
		d, err2 := jstr.Decode(b)
		rt = rt && err == nil && err2 == nil && d == s
	}
	check("round trip", rt)

	lit := []byte(`"a\uD83D\uDE00z\n` + "\xc3\xa9" + `"`)
	want, _ := jstr.Decode(lit)
	split := true
	for i := 0; i <= len(lit); i++ {
		d := new(jstr.Decoder)
		err := d.Write(lit[:i])
		got := ""
		if err == nil {
			err = d.Write(lit[i:])
		}
		if err == nil {
			got, err = d.Close()
		}
		split = split && err == nil && got == want
	}
	check("all split points", split)

	big := []byte{'"'}
	for len(big) < 1<<20 {
		big = append(big, `\uD83D\uDE00`...)
	}
	big = append(big, '"')
	before := jstr.Checked()
	d := new(jstr.Decoder)
	for _, b := range big {
		d.Write([]byte{b})
	}
	_, err = d.Close()
	check("byte budget <=2x", err == nil && jstr.Checked()-before <= 2*int64(len(big)))

	fmt.Printf("total: %d failure(s)\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
