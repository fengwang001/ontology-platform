package qp_test

import (
	"bytes"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"ontology/qp"
)

func derr(e error) *qp.DecodeError {
	var d *qp.DecodeError
	errors.As(e, &d)
	return d
}

func TestEncode(t *testing.T) {
	r := strings.Repeat
	for _, c := range [][2]string{
		{"hello", "hello"}, {"a=b", "a=3Db"}, {"a\tb", "a\tb"},
		{"a \n", "a=20\r\n"}, {"a b", "a b"}, {"a ", "a=20"}, {"a\t", "a=09"},
		{"a\r\nb\r\n", "a\r\nb\r\n"}, {"a\rb", "a=0Db"},
		{"a\xc3\xa9a", "a=C3=A9a"}, {"\x00", "=00"}, {"", ""},
		{r("a", 74) + "=", r("a", 74) + "=\r\n=3D"},
		{r("a", 76), r("a", 76)}, {r("a", 77), r("a", 75) + "=\r\naa"},
		{r("a", 75) + " b", r("a", 75) + "=\r\n b"},
	} {
		enc := qp.Encode([]byte(c[0]))
		if string(enc) != c[1] {
			t.Errorf("Encode(%q)=\n %q\nwant %q", c[0], enc, c[1])
		}
		for _, l := range strings.Split(strings.TrimRight(string(enc), "\r\n"), "\r\n") {
			if len(l) > 76 {
				t.Errorf("line %d >76: %q", len(l), l)
			}
		}
	}
}

func TestDecodeHappy(t *testing.T) {
	r := strings.Repeat
	for _, c := range [][2]string{
		{"hello\r\n", "hello\r\n"}, {"a=3Db", "a=b"}, {"a=\r\nb", "ab"},
		{"a=20b", "a b"}, {"=41", "A"}, {r("a", 76) + "=\r\nb", r("a", 76) + "b"},
	} {
		if got, err := qp.Decode([]byte(c[0])); err != nil || string(got) != c[1] {
			t.Errorf("Decode(%q)=%q,%v want %q", c[0], got, err, c[1])
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	sen := []error{
		qp.ErrBadEscape, qp.ErrUnexpectedEOF, qp.ErrTrailingEquals,
		qp.ErrLineTooLong, qp.ErrInvalidByte, qp.ErrTrailingWhitespace, qp.ErrBadLineEnding,
	}
	r := strings.Repeat
	for _, c := range [][3]any{
		{"a=xb", 0, 1}, {"a=\n", 0, 1}, {"a=\rx", 0, 1}, {"a=4", 1, 2},
		{"a=", 2, 1}, {r("a", 77) + "\r\n", 3, 76},
		{"a\x01b", 4, 1}, {"a \r\n", 5, 1}, {"a\t", 5, 1},
		{"a\rb", 6, 1}, {"a\nb", 6, 1},
	} {
		_, err := qp.Decode([]byte(c[0].(string)))
		d, want, off := derr(err), sen[c[1].(int)], c[2].(int)
		if d == nil || !errors.Is(err, want) || d.Offset != off {
			t.Errorf("Decode(%q)=%v want %v@%d", c[0], err, want, off)
		}
	}
}

func sameErr(a, b error) bool {
	da, db := derr(a), derr(b)
	if da == nil || db == nil {
		return a == b
	}
	return da.Offset == db.Offset && errors.Is(da, db.Err)
}

func TestSplits(t *testing.T) {
	r := strings.Repeat
	for _, in := range []string{
		"a=3Db\r\nc=20d", "=\r\n", "a=\r", "=4", "=41", "a\r\nb",
		r("a", 76) + "=\r\nb", "a=0Db", "=3D=3D", "a \r\nb", "a=", "=41=0D",
	} {
		ref, refErr := qp.Decode([]byte(in))
		ok := func(out []byte, err error) bool {
			return (err == nil && refErr == nil && bytes.Equal(out, ref)) ||
				(err != nil && refErr != nil && sameErr(err, refErr))
		}
		for cut := 0; cut <= len(in); cut++ {
			d := qp.NewDecoder()
			_, err := d.Write([]byte(in[:cut]))
			if err == nil {
				_, err = d.Write([]byte(in[cut:]))
			}
			if err == nil {
				err = d.Close()
			}
			if !ok(d.Output(), err) {
				t.Fatalf("split %q@%s: %v vs %v", in, strconv.Itoa(cut), err, refErr)
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		x := make([]byte, rng.Intn(200))
		rng.Read(x)
		dec, err := qp.Decode(qp.Encode(x))
		want := bytes.ReplaceAll(x, []byte{'\n'}, []byte("\r\n"))
		if err != nil || !bytes.Equal(dec, want) {
			t.Fatalf("rt fail x=%x err=%v", x, err)
		}
	}
	for n := 75; n <= 77; n++ {
		x := []byte(strings.Repeat("~", n))
		if dec, err := qp.Decode(qp.Encode(x)); err != nil || !bytes.Equal(dec, x) {
			t.Fatalf("len %d: %q %v", n, dec, err)
		}
	}
}

func TestMinimal(t *testing.T) {
	dec, _ := qp.Decode([]byte("a=41b\r\n"))
	if string(qp.Encode(dec)) != "aAb\r\n" {
		t.Fatalf("redundant escape not collapsed: %q", qp.Encode(dec))
	}
	for _, s := range []string{"a b\tc=3Dd\r\n", strings.Repeat("a", 76) + "\r\n"} {
		d, err := qp.Decode([]byte(s))
		if err != nil || string(qp.Encode(d)) != s {
			t.Fatalf("unstable: %q,%v", qp.Encode(d), err)
		}
	}
}

func TestCounter(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	x := make([]byte, 1<<20)
	rng.Read(x)
	if _, n := qp.EncodeCount(x); n > 2*len(x) {
		t.Fatalf("inspections %d > 2*%d", n, len(x))
	}
}
