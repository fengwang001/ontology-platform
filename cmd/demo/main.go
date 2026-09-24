// Command demo 逐条验证严格模式流式 Base64 编解码器的语义。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/stream"
)

var fails int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Println(status, name)
}

func decode(mime bool, max int, in string) ([]byte, error) {
	d := stream.NewDecoder(mime, max)
	if _, err := d.Write([]byte(in)); err != nil {
		return d.Output(), err
	}
	return d.Output(), d.Close()
}

func errKey(err error) string {
	var se *stream.Error
	if errors.As(err, &se) {
		return fmt.Sprintf("%v@%d", se.Kind, se.Off)
	}
	return fmt.Sprint(err)
}

func canonical() bool {
	cases := []struct{ in, want string }{
		{"QQ==", "A"}, {"QUI=", "AB"}, {"", ""},
	}
	for _, c := range cases {
		if out, err := decode(false, 0, c.in); err != nil || string(out) != c.want {
			return false
		}
	}
	for _, in := range []string{"QR==", "QUJ=", "QQ", "QQ=", "QQ===", "QQ==QQ=="} {
		if _, err := decode(false, 0, in); err == nil {
			return false
		}
	}
	return true
}

func newlines() bool {
	for _, in := range []string{"Zm9v\r\nYmFy", "Zm9v\nYmFy", "Zm9v\r\n"} {
		if _, err := decode(true, 0, in); err != nil {
			return false
		}
	}
	for _, in := range []string{"Zm\n9v", "Zm9v\r", "Zm9v\rx", "Zm9v Yg==", "Zm9v\tYg=="} {
		if _, err := decode(true, 0, in); err == nil {
			return false
		}
	}
	_, err := decode(false, 0, "Zm9v\n")
	return err != nil
}

func errorKinds() bool {
	cases := []struct {
		in   string
		kind  error
		off  int64
	}{
		{"Zm$v", stream.ErrIllegalChar, 2},
		{"QR==", stream.ErrNonCanonical, 1},
		{"QQ==QQ==", stream.ErrPadding, 4},
		{"QQ", stream.ErrLength, 2},
		{"Zm\n9v", stream.ErrNewline, 2},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		_, err := decode(true, 0, c.in)
		var se *stream.Error
		if !errors.Is(err, c.kind) || !errors.As(err, &se) || se.Off != c.off {
			return false
		}
		seen[c.kind] = true
	}
	return len(seen) == 5
}

func splits() bool {
	inputs := []string{"", "QQ==", "Zm9vYmFy", "QR==", "QQ==QQ==", "Zm\n9v", "QQ", "Zm9v\r\nYmFy"}
	for _, in := range inputs {
		out0, err0 := decode(true, 0, in)
		want := string(out0) + "|" + errKey(err0)
		for k := 0; k <= len(in); k++ {
			if got := runSplit(in, k); got != want {
				return false
			}
		}
	}
	return true
}

func runSplit(in string, k int) string {
	d := stream.NewDecoder(true, 0)
	var err error
	for _, part := range []string{in[:k], in[k:]} {
		if _, err = d.Write([]byte(part)); err != nil {
			break
		}
	}
	if err == nil {
		err = d.Close()
	}
	return string(d.Output()) + "|" + errKey(err)
}

func roundtrip() bool {
	samples := [][]byte{{}, {0}, []byte("f"), []byte("fo"), []byte("foo"), []byte("foobar")}
	for _, x := range samples {
		for _, mime := range []bool{false, true} {
			e := stream.NewEncoder(mime)
			e.Write(x)
			e.Close()
			y := string(e.Output())
			out, err := decode(mime, 0, y)
			if err != nil || !bytes.Equal(out, x) || reencode(mime, out) != y {
				return false
			}
		}
	}
	return true
}

func reencode(mime bool, p []byte) string {
	e := stream.NewEncoder(mime)
	e.Write(p)
	e.Close()
	return string(e.Output())
}

func limit() bool {
	d := stream.NewDecoder(false, 4)
	_, err := d.Write([]byte("Zm9vYmFy"))
	if !errors.Is(err, stream.ErrLimit) || string(d.Output()) != "foo" {
		return false
	}
	_, err = d.Write([]byte("Zm9v"))
	return errors.Is(err, stream.ErrLimit)
}

func counter() bool {
	in := strings.Repeat("Zm9v", 1<<18) // 1 MiB
	d := stream.NewDecoder(false, 0)
	for i := 0; i < len(in); i++ {
		if _, err := d.Write([]byte(in[i : i+1])); err != nil {
			return false
		}
	}
	return d.Close() == nil && d.Checked() == int64(len(in))
}

func main() {
	check("canonical-samples", canonical())
	check("newline-positions", newlines())
	check("error-kinds-offsets", errorKinds())
	check("split-consistency", splits())
	check("roundtrip", roundtrip())
	check("output-limit", limit())
	check("checked-counter", counter())
	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
