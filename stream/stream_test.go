package stream_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/field"
	"ontology/linescan"
	"ontology/stream"
)

func feed(t *testing.T, p *stream.Parser, s string, n int) []stream.Event {
	t.Helper()
	var evs []stream.Event
	for i := 0; i < len(s); i += n {
		es, err := p.Write([]byte(s[i:min(i+n, len(s))]))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, es...)
	}
	return evs
}

func TestLineEndings(t *testing.T) {
	got, _ := linescan.New(0).Feed([]byte("a\nb\r\nc\rd\n"))
	if !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("lines %v", got)
	}
	if evs := feed(t, stream.New(0, 0), "data: x\r\n\r\ndata: y\r\n\r\n", 64); len(evs) != 2 {
		t.Fatalf("CRLF counted as two blank lines: %v", evs)
	}
	got, _ = linescan.New(0).Feed([]byte("a\n\xef\xbb\xbfb\n"))
	if !reflect.DeepEqual(got, []string{"a", "\xef\xbb\xbfb"}) {
		t.Fatalf("mid-stream BOM eaten: %v", got)
	}
}
func TestCRLFChunkBoundary(t *testing.T) {
	p := stream.New(0, 0)
	if evs, _ := p.Write([]byte("data: x\r")); len(evs) != 0 {
		t.Fatalf("early dispatch %v", evs)
	}
	evs, _ := p.Write([]byte("\n\r\n"))
	if len(evs) != 1 || evs[0].Data != "x" {
		t.Fatalf("split CRLF: %v", evs)
	}
}
func TestAllSplitPoints(t *testing.T) {
	const doc = "\xef\xbb\xbf: c\r\ndata: a\ndata: b\revent: m\nid: 9\r\n\r\ndata: z\n\n"
	want := feed(t, stream.New(0, 0), doc, len(doc))
	if len(want) != 2 || want[0].Data != "a\nb" || want[0].Name != "m" || want[0].ID != "9" {
		t.Fatalf("baseline %v", want)
	}
	for n := 1; n < len(doc); n++ {
		if got := feed(t, stream.New(0, 0), doc, n); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk size %d: %v", n, got)
		}
	}
}
func TestFieldRules(t *testing.T) {
	cases := []struct{ in, name, val string }{
		{"data: x", "data", "x"}, {"data:x", "data", "x"},
		{"data:  x", "data", " x"}, {"data:", "data", ""},
		{"data", "data", ""}, {": hi", "", "hi"},
	}
	for _, c := range cases {
		if f := field.Parse(c.in); f.Name != c.name || f.Value != c.val {
			t.Errorf("Parse(%q) = %q %q", c.in, f.Name, f.Value)
		}
	}
}
func TestDispatchRules(t *testing.T) {
	if evs := feed(t, stream.New(0, 0), ": c\n\nfoo: 1\n\n", 3); len(evs) != 0 {
		t.Fatalf("comment/unknown dispatched %v", evs)
	}
	evs := feed(t, stream.New(0, 0), "data:a\ndata:b\n\nevent: m\n\n", 5)
	if len(evs) != 2 || evs[0].Data != "a\nb" || evs[1].Name != "m" || evs[1].Data != "" {
		t.Fatalf("dispatch %v", evs)
	}
}
func TestIDAndRetry(t *testing.T) {
	p := stream.New(0, 0)
	feed(t, p, "id: 1\nretry: 250\ndata: a\n\n", 4)
	feed(t, p, "id: x\x00y\nretry: 12x\nretry: -5\nretry: \ndata: b\n\n", 4)
	if p.LastEventID() != "1" || p.Retry() != 250 ||
		p.LastEventID() != p.LastEventID() || p.Retry() != p.Retry() {
		t.Fatalf("id=%q retry=%d", p.LastEventID(), p.Retry())
	}
}
func TestLimits(t *testing.T) {
	p := stream.New(8, 3)
	feed(t, p, "data: ab\n", 9)
	_, errLine := p.Write([]byte("toolonggg\n"))
	_, errData := p.Write([]byte("data: cd\n"))
	if !errors.Is(errLine, linescan.ErrLineTooLong) || !errors.Is(errData, stream.ErrDataTooLarge) ||
		errors.Is(errLine, stream.ErrDataTooLarge) || errors.Is(errData, linescan.ErrLineTooLong) {
		t.Fatalf("errors %v %v", errLine, errData)
	}
	if p.LastEventID() != "" || p.Retry() != stream.DefaultRetry {
		t.Fatal("state changed by rejected input")
	}
	evs, _ := p.End()
	if len(evs) != 1 || evs[0].Data != "ab" {
		t.Fatalf("aggregated state lost: %v", evs)
	}
}
func TestEOFAndReset(t *testing.T) {
	p := stream.New(0, 0)
	if evs := feed(t, p, "id: 7\nretry: 100\ndata: a\n\ndata: x\n", 2); len(evs) != 1 {
		t.Fatalf("auto dispatch at EOF: %v", evs)
	}
	p.Reset()
	if p.LastEventID() != "7" || p.Retry() != 100 {
		t.Fatal("reset lost reconnect state")
	}
	if evs := feed(t, p, "data: b\n\n", 9); len(evs) != 1 || evs[0].Data != "b" {
		t.Fatalf("reset kept aggregation: %v", evs)
	}
}
