package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/field"
	"ontology/linescan"
	"ontology/stream"
)

var checks []bool

func check(name string, ok bool) {
	checks = append(checks, ok)
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func scanAll(chunks ...[]byte) []string {
	sc := linescan.New(0)
	var lines []string
	for _, c := range chunks {
		must(sc.Feed(c, func(l []byte) error { lines = append(lines, string(l)); return nil }))
	}
	return lines
}

func parse(s string) *stream.Parser {
	p := stream.New(0, 0)
	must(p.Feed([]byte(s)))
	return p
}

func main() {
	check("three line endings", reflect.DeepEqual(
		scanAll([]byte("a\nb\r\nc\r")), []string{"a", "b", "c"}))
	check("crlf is one ending", reflect.DeepEqual(
		scanAll([]byte("x\r\n\r\ny\r\n")), []string{"x", "", "y"}))
	check("chunk split inside crlf", reflect.DeepEqual(
		scanAll([]byte("x\r"), []byte("\ny\n")), []string{"x", "y"}))

	src := []byte("aa\r\nbb\ncc\rdd\r\n")
	want, same := scanAll(src), true
	for n := 1; n < len(src) && same; n++ {
		var got []string
		sc := linescan.New(0)
		for i := 0; i < len(src); i += n {
			must(sc.Feed(src[i:min(i+n, len(src))],
				func(l []byte) error { got = append(got, string(l)); return nil }))
		}
		same = reflect.DeepEqual(got, want)
	}
	check("all split points identical", same)

	check("bom only at stream head", reflect.DeepEqual(
		scanAll([]byte("\xEF\xBB"), []byte("\xBFdata\n\xEF\xBB\xBFx\n")),
		[]string{"data", "\xEF\xBB\xBFx"}))

	space := true
	for _, c := range [][2]string{
		{"data: x", "x"}, {"data:x", "x"}, {"data:  x", " x"},
		{"data:", ""}, {"data", ""},
	} {
		f, ok := field.Parse([]byte(c[0]))
		space = space && ok && f.Name == "data" && f.Value == c[1]
	}
	_, isField := field.Parse([]byte(":comment"))
	check("colon space rule", space && !isField)

	ev := parse("data:a\ndata:b\n\n").Events()
	check("multi data join", len(ev) == 1 && ev[0].Data == "a\nb")
	check("comment block no dispatch", len(parse(":ping\nunknown: 1\n\n").Events()) == 0)
	check("id with NUL ignored",
		parse("id: good\ndata: x\n\nid: ba\x00d\ndata: y\n\n").LastEventID() == "good")
	check("invalid retry keeps state", parse("retry: 500\n\nretry: 12x\n\nretry: \n\n").Retry() == 500)

	pl, pd := stream.New(8, 0), stream.New(0, 3)
	must(pl.Feed([]byte("id: q\n\n")))
	must(pd.Feed([]byte("id: s\nretry: 42\n\n")))
	errL, errD := pl.Feed([]byte("0123456789\n")), pd.Feed([]byte("data: abcd\n\n"))
	check("two distinct limits keep state",
		errors.Is(errL, linescan.ErrLineTooLong) && !errors.Is(errL, stream.ErrDataTooLong) &&
			errors.Is(errD, stream.ErrDataTooLong) && !errors.Is(errD, linescan.ErrLineTooLong) &&
			pl.LastEventID() == "q" && pd.LastEventID() == "s" && pd.Retry() == 42 &&
			len(pl.Events()) == 0 && len(pd.Events()) == 0)

	tail := parse("data: tail\n")
	quiet := len(tail.Events()) == 0
	must(tail.Close())
	ev = tail.Events()
	check("no auto dispatch at eof", quiet && len(ev) == 1 && ev[0].Data == "tail")

	q := parse("id: z\nretry: 7\ndata: x\n\n")
	check("repeated queries consistent", q.LastEventID() == "z" && q.Retry() == 7 &&
		q.LastEventID() == q.LastEventID() && q.Retry() == q.Retry())

	pass := 0
	for _, ok := range checks {
		if ok {
			pass++
		}
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}
