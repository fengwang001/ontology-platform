package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/qp"
)

func main() {
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
			fail++
		}
	}

	check("trailing whitespace cases",
		string(qp.Encode([]byte("a \n"))) == "a=20\r\n" &&
			string(qp.Encode([]byte("a b"))) == "a b" &&
			string(qp.Encode([]byte("a "))) == "a=20")

	atom := strings.Repeat("a", 74) + "="
	check("=XX never split",
		string(qp.Encode([]byte(atom))) == strings.Repeat("a", 74)+"=\r\n=3D")

	limit := qp.Encode([]byte(strings.Repeat("a", 77)))
	check("76-char limit counts soft-break =",
		string(limit) == strings.Repeat("a", 75)+"=\r\naa" &&
			bytes.Contains([]byte(strings.Repeat("a", 75)), []byte{'='}) == false)

	errCases := []struct{ in, want string }{
		{"=x", "qp: '=' not followed by two hex digits"},
		{"a=", "qp: dangling '=' at end of input"},
		{strings.Repeat("a", 77), "qp: encoded line exceeds 76 characters"},
		{"\x01", "qp: unescaped byte outside printable ASCII"},
		{" \r\n", "qp: unescaped whitespace at end of line"},
	}
	ok := true
	for _, c := range errCases {
		_, err := qp.Decode([]byte(c.in))
		var de *qp.DecodeError
		if !errors.As(err, &de) || !strings.Contains(err.Error(), c.want[4:]) || de.Offset < 0 {
			ok = false
		}
	}
	check("five distinguishable errors with offsets", ok)

	ok = true
	for _, in := range []string{"=3D\r\na", "a=\r\nb", "=41=0D", strings.Repeat("a", 76) + "=\r\nb"} {
		ref, refErr := qp.Decode([]byte(in))
		for cut := 0; cut <= len(in); cut++ {
			d := qp.NewDecoder()
			_, e1 := d.Write([]byte(in[:cut]))
			var e error
			if e1 == nil {
				_, e = d.Write([]byte(in[cut:]))
				if e == nil {
					e = d.Close()
				}
			}
			if (e == nil) != (refErr == nil) || (e == nil && !bytes.Equal(d.Output(), ref)) {
				ok = false
			}
		}
	}
	check("all split points consistent", ok)

	ok = true
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100; i++ {
		x := make([]byte, rng.Intn(150))
		rng.Read(x)
		dec, err := qp.Decode(qp.Encode(x))
		if err != nil {
			ok = false
			break
		}
		want := bytes.ReplaceAll(x, []byte{'\n'}, []byte("\r\n"))
		if !bytes.Equal(dec, want) {
			ok = false
			break
		}
	}
	check("round trip", ok)

	y := []byte("a=41b\r\n")
	dec, _ := qp.Decode(y)
	check("minimal escaping", string(qp.Encode(dec)) == "aAb\r\n")

	big := make([]byte, 1<<20)
	rng.Read(big)
	_, n := qp.EncodeCount(big)
	check("inspection counter", n <= 2*len(big))

	if fail == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAILURES\n", fail)
		os.Exit(1)
	}
	_ = bytes.Equal
	_ = qp.Encode
}
