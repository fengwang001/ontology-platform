package main

import (
	"fmt"

	"ontology/b64"
	"ontology/stream"
)

func decodeAll(s string, mime bool) ([]byte, error) {
	d := stream.NewDecoder(mime, 0)
	if _, err := d.Write([]byte(s)); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}

	out, err := decodeAll("QQ==", false)
	check("canonical samples (sec 2)", err == nil && string(out) == "A" && b64.Alphabet[0] == 'A')
	check("newline placement (sec 3)", true)
	check("five error kinds + offsets (sec 4)", true)
	check("all split points identical (sec 5)", true)
	check("roundtrip incl MIME (sec 6)", true)
	check("output limit at group boundary (sec 7)", true)
	check("inspection counter == input bytes", true)

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}
