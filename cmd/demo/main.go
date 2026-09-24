package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/jstr"
)

func decErr(lit string) (error, int) {
	_, err := jstr.Decode([]byte(lit))
	var de *jstr.DecodeError
	if errors.As(err, &de) {
		return de.Kind, de.Offset
	}
	return err, -1
}

func expect(lit string, want error, wantOff int) bool {
	got, off := decErr(lit)
	return errors.Is(got, want) && off == wantOff
}

func splitAgree(lit string) bool {
	p := []byte(lit)
	_, w := jstr.Decode(p)
	for k := 0; k <= len(p); k++ {
		d := jstr.NewDecoder()
		var e error
		if _, e = d.Write(p[:k]); e == nil {
			_, e = d.Write(p[k:])
			if e == nil {
				_, e = d.Result()
			}
		}
		if errors.Is(e, jstr.ErrSurrogate) != errors.Is(w, jstr.ErrSurrogate) {
			return false
		}
	}
	return true
}

func main() {
	type C struct {
		name string
		ok   bool
		info string
	}
	cs := []C{
		{"errors+offset",
			expect("\"a\x01\"", jstr.ErrControl, 2) &&
				expect(`"\x"`, jstr.ErrUnknownEscape, 1) &&
				expect(`"\u12"`, jstr.ErrBadHex, 1) &&
				expect(`"abc`, jstr.ErrUnterminated, 4) &&
				expect(`"a"x`, jstr.ErrTrailingBytes, 3),
			"five classes point to byte"},
		{"surrogates",
			smile() &&
				expect(`"\uD83D"`, jstr.ErrSurrogate, 1) &&
				expect(`"\uDE00"`, jstr.ErrSurrogate, 1) &&
				expect(`"\uD83Dx"`, jstr.ErrSurrogate, 1) &&
				expect(`"\uD83DA"`, jstr.ErrSurrogate, 1) &&
				expect(`"\uDE00\uD83D"`, jstr.ErrSurrogate, 1),
			"grin ok; bad pairs @1"},
		{"invalid-utf8",
			expect("\"\xff\"", jstr.ErrInvalidUTF8, 1),
			"decode rejects raw 0xFF"},
		{"encode-invalid",
			func() bool {
				_, err := jstr.Encode("a\xffb")
				var ee *jstr.EncodeError
				return errors.As(err, &ee) && ee.Offset == 1
			}(), "EncodeError offset 1"},
		{"minimal-escape",
			func() bool {
				y, err := jstr.Encode("/\x7f\u2028")
				return err == nil && string(y) == `"/`+"\x7f\u2028\""
			}(), "slash/DEL/U+2028 raw"},
		{"roundtrip",
			func() bool {
				s := "a\"\\\x00\x1f\t😀/中"
				y, err := jstr.Encode(s)
				if err != nil {
					return false
				}
				b, err := jstr.Decode(y)
				return err == nil && b == s
			}(), "Decode(Encode(s))==s"},
		{"splits",
			splitAgree(`"\uD83D\uDE00"`) && splitAgree(`"\uD83Dx"`) &&
				splitAgree(`"a😀b"`) && splitAgree(`"unterminated`),
			"all cut points agree"},
	}

	big := `"` + strings.Repeat(`\uD83D\uDE00`, 50000) + `"`
	d := jstr.NewDecoder()
	for i := 0; i < len(big); i++ {
		if _, err := d.Write([]byte{big[i]}); err != nil {
			panic(err)
		}
	}
	if _, err := d.Result(); err != nil {
		panic(err)
	}
	cs = append(cs, C{"examined", d.Examined() <= 2*len(big),
		fmt.Sprintf("%d <= %d", d.Examined(), 2*len(big))})

	pass := 0
	for _, c := range cs {
		tag := "OK  "
		if !c.ok {
			tag = "FAIL"
		}
		fmt.Printf("%s %-15s %s\n", tag, c.name, c.info)
		if c.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(cs))
}

func smile() bool {
	s, err := jstr.Decode([]byte(`"😀"`))
	if err != nil {
		return false
	}
	y, err := jstr.Decode([]byte(`"\uD83D\uDE00"`))
	return err == nil && s == y && s == "😀"
}
