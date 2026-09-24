// Command demo 逐条演示严格模式流式 Base64 编解码器的语义判定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/b64"
	"ontology/stream"
)

func decode(mime bool, chunks ...string) ([]byte, error) {
	d := stream.NewDecoder(mime, -1)
	for _, c := range chunks {
		if _, err := d.Write([]byte(c)); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func main() {
	names := []string{"canonical-samples", "newline-rules", "error-kinds+offsets",
		"split-invariance", "roundtrip", "output-limit", "checked-counter"}
	oks := []bool{checkSamples(), checkNewlines(), checkErrorKinds(),
		checkSplit(), checkRoundTrip(), checkLimit(), checkCounter()}
	pass := 0
	for i, ok := range oks {
		status := "FAIL"
		if ok {
			status, pass = "OK", pass+1
		}
		fmt.Println(status, names[i])
	}
	fmt.Printf("SUMMARY %d/%d OK\n", pass, len(oks))
}

func checkSamples() bool {
	ins := []string{"QQ==", "QR==", "QUI=", "QUJ=", "QQ", "QQ=", "QQ===", "QQ==QQ==", ""}
	outs := []string{"A", "", "AB", "", "", "", "", "", ""}
	oks := []bool{true, false, true, false, false, false, false, false, true}
	for i := range ins {
		out, err := decode(false, ins[i])
		if (err == nil) != oks[i] || oks[i] && string(out) != outs[i] {
			return false
		}
	}
	return true
}

func checkNewlines() bool {
	good := []string{"QUJD\r\nREU=", "QUJD\nREU=", "\r\nQQ=="}
	bad := []string{"QU\nJD", "QUJD\r", "QU JD", "QU\tJD", "QQ==\rQ"}
	for _, s := range good {
		if _, err := decode(true, s); err != nil {
			return false
		}
	}
	for _, s := range bad {
		if _, err := decode(true, s); err == nil {
			return false
		}
	}
	_, err := decode(false, "QUJD\nREU=")
	return err != nil
}

func checkErrorKinds() bool {
	ins := []string{"Q!I=", "QR==", "QQ==QQ==", "QQ", "QU\nJD"}
	mimes := []bool{false, false, false, false, true}
	kinds := []error{b64.ErrInvalidChar, b64.ErrNonCanonical, b64.ErrBadPadding, stream.ErrLength, stream.ErrNewline}
	offs := []int64{1, 1, 4, 2, 2}
	for i := range ins {
		_, err := decode(mimes[i], ins[i])
		var se *stream.Error
		if !errors.As(err, &se) || se.Kind != kinds[i] || se.Off != offs[i] {
			return false
		}
		if errors.Is(err, kinds[(i+1)%len(kinds)]) {
			return false
		}
	}
	return true
}

func checkSplit() bool {
	key := func(err error) string {
		var se *stream.Error
		if errors.As(err, &se) {
			return fmt.Sprintf("%v@%d", se.Kind, se.Off)
		}
		return "ok"
	}
	for _, in := range []string{"", "QQ==", "QR==", "QUJD\r\nREU=", "QQ==QQ==", "QQ"} {
		wantOut, wantErr := decode(true, in)
		for i := 0; i <= len(in); i++ {
			out, err := decode(true, in[:i], in[i:])
			if string(out) != string(wantOut) || key(err) != key(wantErr) {
				return false
			}
		}
	}
	return true
}

func checkRoundTrip() bool {
	for _, n := range []int{0, 1, 2, 3, 5, 60, 61} {
		x := make([]byte, n)
		for i := range x {
			x[i] = byte(i*7 + n)
		}
		for _, mime := range []bool{false, true} {
			e := stream.NewEncoder(mime)
			e.Write(x)
			e.Close()
			y := string(e.Output())
			out, err := decode(mime, y)
			e2 := stream.NewEncoder(mime)
			e2.Write(out)
			e2.Close()
			if err != nil || string(out) != string(x) || string(e2.Output()) != y {
				return false
			}
		}
	}
	return true
}

func checkLimit() bool {
	d := stream.NewDecoder(false, 3)
	_, err := d.Write([]byte("QUJDRA=="))
	var se *stream.Error
	if !errors.As(err, &se) || se.Kind != stream.ErrTooLong || se.Off != 4 {
		return false
	}
	if _, err := d.Write([]byte("QQ==")); err == nil {
		return false
	}
	return string(d.Output()) == "ABC"
}

func checkCounter() bool {
	in := bytes.Repeat([]byte{'A'}, 1<<20)
	d := stream.NewDecoder(false, -1)
	for i := range in {
		d.Write(in[i : i+1])
	}
	return d.Close() == nil && d.Checked() == 1<<20 && len(d.Output()) == 3*(1<<20)/4
}
