package stream_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/field"
	"ontology/linescan"
	"ontology/stream"
)

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func scan(t *testing.T, chunks ...string) []string {
	sc := linescan.New(0)
	var out []string
	for _, c := range chunks {
		must(t, sc.Feed([]byte(c), func(l []byte) error { out = append(out, string(l)); return nil }))
	}
	return out
}
func parse(t *testing.T, s string) *stream.Parser {
	p := stream.New(0, 0)
	must(t, p.Feed([]byte(s)))
	return p
}
func TestLineEndings(t *testing.T) {
	for i, c := range []struct{ in, want []string }{
		{[]string{"a\nb\r\nc\r"}, []string{"a", "b", "c"}},
		{[]string{"x\r\n\r\ny\r\n"}, []string{"x", "", "y"}},
		{[]string{"x\r", "\ny\n"}, []string{"x", "y"}},
		{[]string{"\xEF\xBB", "\xBFdata: x\n"}, []string{"data: x"}},
	} {
		if got := scan(t, c.in...); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("case %d: got %q", i, got)
		}
	}
}
func TestAllSplitPoints(t *testing.T) {
	src := "id: 1\nevent: hi\ndata: a\ndata: b\n\n: ping\nretry: 250\ndata: c\r\n\r\n"
	want := parse(t, src).Events()
	for n := 1; n <= len(src); n++ {
		p := stream.New(0, 0)
		for i := 0; i < len(src); i += n {
			must(t, p.Feed([]byte(src[i:min(i+n, len(src))])))
		}
		if got := p.Events(); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d: got %v want %v", n, got, want)
		}
	}
}
func TestFieldsAndDispatch(t *testing.T) {
	for in, w := range map[string][2]string{
		"data: x": {"data", "x"}, "data:x": {"data", "x"}, "data:  x": {"data", " x"},
		"data:": {"data", ""}, "data": {"data", ""},
	} {
		if f, ok := field.Parse([]byte(in)); !ok || f.Name != w[0] || f.Value != w[1] {
			t.Fatalf("%q -> %v,%v", in, f, ok)
		}
	}
	if _, ok := field.Parse([]byte(":c")); ok {
		t.Fatal("comment parsed as field")
	}
	if ev := parse(t, "data:a\ndata:b\n\n").Events(); len(ev) != 1 || ev[0].Data != "a\nb" {
		t.Fatalf("multi data join: %v", ev)
	}
	if n := len(parse(t, ": ping\nunknown: 1\n\n").Events()); n != 0 {
		t.Fatalf("comment/unknown-only block dispatched %d", n)
	}
	if ev := parse(t, "data: a\n\n\xEF\xBB\xBFdata: b\n\n").Events(); len(ev) != 1 {
		t.Fatalf("middle BOM became special: %v", ev)
	}
	p := parse(t, "id: good\ndata: x\n\nid: ba\x00d\ndata: y\n\n")
	if p.LastEventID() != "good" {
		t.Fatalf("NUL id not ignored: %q", p.LastEventID())
	}
	q := parse(t, "retry: 500\n\nretry: 12x\n\nretry: \n\nretry: -3\n\n")
	if q.Retry() != 500 {
		t.Fatalf("invalid retry changed state: %d", q.Retry())
	}
	if q.Retry() != q.Retry() || p.LastEventID() != p.LastEventID() {
		t.Fatal("repeated queries differ")
	}
}
func TestLimits(t *testing.T) {
	pl, pd := stream.New(8, 0), stream.New(0, 3)
	must(t, pl.Feed([]byte("id: q\n\n")))
	must(t, pd.Feed([]byte("id: s\nretry: 42\n\n")))
	errL, errD := pl.Feed([]byte("0123456789\n")), pd.Feed([]byte("data: abcd\n\n"))
	if !errors.Is(errL, linescan.ErrLineTooLong) || errors.Is(errL, stream.ErrDataTooLong) ||
		!errors.Is(errD, stream.ErrDataTooLong) || errors.Is(errD, linescan.ErrLineTooLong) {
		t.Fatalf("limit errors not distinguishable: %v %v", errL, errD)
	}
	if pl.LastEventID() != "q" || pd.LastEventID() != "s" || pd.Retry() != 42 {
		t.Fatal("rejection changed aggregated state")
	}
	if len(pl.Events())+len(pd.Events()) != 0 {
		t.Fatal("rejection dispatched events")
	}
}
func TestEOFTailAndReset(t *testing.T) {
	p := parse(t, "data: tail\n")
	if n := len(p.Events()); n != 0 {
		t.Fatalf("tail auto-dispatched %d events", n)
	}
	must(t, p.Close())
	if ev := p.Events(); len(ev) != 1 || ev[0].Data != "tail" {
		t.Fatalf("close did not dispatch tail: %v", ev)
	}
	r := parse(t, "id: r1\nretry: 99\ndata: x\n\n")
	r.Reset()
	must(t, r.Feed([]byte("data: y\n\n")))
	if ev := r.Events(); len(ev) != 1 || ev[0].ID != "r1" || r.Retry() != 99 {
		t.Fatalf("reset lost reconnect state: %v retry=%d", ev, r.Retry())
	}
}
