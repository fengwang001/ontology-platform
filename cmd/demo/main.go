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
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
	checks = append(checks, ok)
}

func main() {
	lines := func(s string, n int) []string {
		sc := linescan.New(0)
		var out []string
		for i := 0; i < len(s); i += n {
			ls, _ := sc.Feed([]byte(s[i:min(i+n, len(s))]))
			out = append(out, ls...)
		}
		return out
	}
	check("three line endings", reflect.DeepEqual(
		lines("a\nb\r\nc\rd\n", 64), []string{"a", "b", "c", "d"}))
	check("CRLF is one line end", reflect.DeepEqual(
		lines("x\r\n\r\n", 64), []string{"x", ""}))
	check("chunk split inside CRLF", reflect.DeepEqual(
		lines("x\r\ny\n", 2), []string{"x", "y"}))
	splitOK := true
	const doc = "a: 1\r\nb:2\rc:3\n\nd:4\r\n"
	for n := 1; n <= len(doc); n++ {
		if !reflect.DeepEqual(lines(doc, n), lines(doc, len(doc))) {
			splitOK = false
		}
	}
	check("all split points identical", splitOK)
	check("BOM only at head", reflect.DeepEqual(
		lines("\xef\xbb\xbfx\xef\xbb\xbfy\n", 1), []string{"x\xef\xbb\xbfy"}))
	colonOK := field.Parse("data: x").Value == "x" &&
		field.Parse("data:x").Value == "x" &&
		field.Parse("data:  x").Value == " x" &&
		field.Parse("data:").Value == "" &&
		field.Parse("data") == field.Field{Name: "data"} &&
		field.Parse(": hi").Name == ""
	check("colon and space rules", colonOK)
	feed := func(p *stream.Parser, s string) []stream.Event {
		evs, _ := p.Write([]byte(s))
		return evs
	}
	check("multi data joined", feed(stream.New(0, 0), "data:a\ndata:b\n\n")[0].Data == "a\nb")
	check("comment not dispatched", len(feed(stream.New(0, 0), ": hi\n\n")) == 0)
	pid := stream.New(0, 0)
	feed(pid, "id: 1\ndata: a\n\nid: x\x00y\ndata: b\n\n")
	check("id with NUL ignored", pid.LastEventID() == "1")
	pr := stream.New(0, 0)
	feed(pr, "retry: 500\nretry: 12x\nretry: abc\n\n")
	check("bad retry keeps state", pr.Retry() == 500)
	pl := stream.New(16, 3)
	feed(pl, "data: ab\n")
	_, errLine := pl.Write([]byte("x: 012345678901234567\n"))
	_, errData := pl.Write([]byte("data: cd\n"))
	endEv, _ := pl.End()
	check("two limit errors", errors.Is(errLine, linescan.ErrLineTooLong) &&
		errors.Is(errData, stream.ErrDataTooLarge) && pl.LastEventID() == "" &&
		len(endEv) == 1 && endEv[0].Data == "ab")
	pe := stream.New(0, 0)
	tail := len(feed(pe, "data: x\n")) == 0
	endEvs, _ := pe.End()
	check("no auto dispatch at EOF", tail && len(endEvs) == 1 && endEvs[0].Data == "x")
	pq := stream.New(0, 0)
	feed(pq, "id: 7\ndata: q\n\n")
	check("queries are stable", pq.LastEventID() == pq.LastEventID() &&
		pq.Retry() == pq.Retry() && pq.LastEventID() == "7")
	passed := 0
	for _, ok := range checks {
		if ok {
			passed++
		}
	}
	fmt.Printf("total %d/%d\n", passed, len(checks))
}
