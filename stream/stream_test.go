package stream_test

import (
	"errors"
	"testing"

	"ontology/field"
	"ontology/linescan"
	"ontology/stream"
)

func feedInto(got *[]stream.Event, p *stream.Parser, input string, n int) []stream.Event {
	for i := 0; i < len(input); i += n {
		j := i + n
		if j > len(input) {
			j = len(input)
		}
		if err := p.Feed([]byte(input[i:j])); err != nil {
			panic(err)
		}
	}
	return *got
}

func newP(got *[]stream.Event, ml, md int) *stream.Parser {
	return stream.New(ml, md, func(e stream.Event) { *got = append(*got, e) })
}

func parse(input string, n int) []stream.Event {
	var got []stream.Event
	p := newP(&got, 0, 0)
	return feedInto(&got, p, input, n)
}

func TestLineEndings(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		got := parse("data: a"+sep+"data: b"+sep+sep, 1)
		if len(got) != 1 || got[0].Data != "a\nb" || got[0].Event != "message" {
			t.Fatalf("sep=%q got=%+v", sep, got)
		}
	}
	if got := parse("\r\n\r\n", 1); len(got) != 0 {
		t.Fatalf("blank CRLFs dispatched %v", got)
	}
	var cross []stream.Event
	p := newP(&cross, 0, 0)
	if err := p.Feed([]byte("data: x\r")); err != nil || len(cross) != 0 {
		t.Fatal("first chunk", err, cross)
	}
	if err := p.Feed([]byte("\ny\n\n")); err != nil || len(cross) != 1 || cross[0].Data != "x" {
		t.Fatal("second chunk", err, cross)
	}
}

func TestSplitInvariant(t *testing.T) {
	in := "id:1\nevent:add\ndata:a\ndata:b\n\n:hb\ndata:c\n\nretry:9\n\n"
	ref := parse(in, len(in))
	if len(ref) != 2 {
		t.Fatalf("ref events=%d", len(ref))
	}
	for n := 1; n <= len(in); n++ {
		got := parse(in, n)
		if len(got) != len(ref) {
			t.Fatalf("chunk=%d len %d!=%d", n, len(got), len(ref))
		}
		for i := range got {
			if got[i] != ref[i] {
				t.Fatalf("chunk=%d event %d %+v != %+v", n, i, got[i], ref[i])
			}
		}
	}
}

func TestFieldParse(t *testing.T) {
	cases := []struct {
		line string
		want field.Field
	}{
		{"data: x", field.Field{Name: "data", Value: "x"}},
		{"data:x", field.Field{Name: "data", Value: "x"}},
		{"data:  x", field.Field{Name: "data", Value: " x"}},
		{"data:", field.Field{Name: "data", Value: ""}},
		{"data", field.Field{Name: "data", Value: ""}},
		{":comment", field.Field{Comment: true}},
	}
	for _, c := range cases {
		if got := field.Parse(c.line); got != c.want {
			t.Fatalf("%q = %+v", c.line, got)
		}
	}
}

func TestDispatchRules(t *testing.T) {
	if got := parse("data:\n\n", 2); len(got) != 1 || got[0].Data != "" {
		t.Fatal("empty data line must dispatch", got)
	}
	if got := parse(":c\n\nunknown:v\n\n", 1); len(got) != 0 {
		t.Fatal("comments/unknown dispatched", got)
	}
	if got := parse("data:a\nunknown:v\n\n", 1); len(got) != 1 || got[0].Data != "a" {
		t.Fatal("unknown field must not block dispatch", got)
	}
	got := parse("event: e\ndata:x\n\ndata:y\n\n", 1)
	if len(got) != 2 || got[0].Event != "e" || got[1].Event != "message" {
		t.Fatal("event name handling", got)
	}
}

func TestIDAndRetry(t *testing.T) {
	var got []stream.Event
	p := newP(&got, 0, 0)
	p.Feed([]byte("id: 1\ndata:init\n\n"))
	if p.LastEventID() != "1" || len(got) != 1 || got[0].ID != "1" {
		t.Fatal("basic id", p.LastEventID(), got)
	}
	p.Feed([]byte("id: a\x00b\ndata:y\n\n"))
	if p.LastEventID() != "1" {
		t.Fatal("NUL id must be ignored", p.LastEventID())
	}
	p.Feed([]byte("retry: 250\n\nretry: 12x\n\nretry:\n\n"))
	if p.Retry() != 250 {
		t.Fatal("illegal retry must not change state", p.Retry())
	}
	p.Reset()
	p.Feed([]byte("data:z\n\n"))
	if p.LastEventID() != "1" || p.Retry() != 250 || len(got) != 3 {
		t.Fatal("reset must keep reconnect state", p.LastEventID(), p.Retry(), len(got))
	}
}

func TestBOM(t *testing.T) {
	if got := parse("\xEF\xBB\xBFdata:bom\n\n", 1); len(got) != 1 || got[0].Data != "bom" {
		t.Fatal("leading BOM", got)
	}
	got := parse("data:\xEF\xBB\xBFmid\n\n", 1)
	if len(got) != 1 || got[0].Data != "\xEF\xBB\xBFmid" {
		t.Fatal("BOM bytes mid-stream must be preserved", got)
	}
	s := linescan.New(0)
	ls1, _ := s.Feed([]byte("\xEF\xBB"))
	ls2, _ := s.Feed([]byte("\xBFdata:x\n\n"))
	if len(ls1) != 0 || len(ls2) != 2 {
		t.Fatal("BOM split across chunks", ls1, ls2)
	}
}

func TestLimitsAreAtomic(t *testing.T) {
	var got []stream.Event
	p := newP(&got, 0, 4)
	p.Feed([]byte("id:z\n\ndata:ab\n"))
	beforeID, beforeRetry := p.LastEventID(), p.Retry()
	if err := p.Feed([]byte("data:cdef\n\n")); !errors.Is(err, stream.ErrDataTooLong) {
		t.Fatal("want ErrDataTooLong", err)
	}
	if p.LastEventID() != beforeID || p.Retry() != beforeRetry || len(got) != 0 {
		t.Fatal("data-limit changed state", p.LastEventID(), got)
	}
	if err := p.Feed([]byte("\n")); err != nil || len(got) != 1 || got[0].Data != "ab" {
		t.Fatal("rolled-back state must dispatch intact", err, got)
	}
	if err := p.Feed([]byte("data:ok\n\n")); err != nil || len(got) != 2 || got[1].Data != "ok" {
		t.Fatal("parser must still work after rejection", err, got)
	}
	p2 := newP(&got, 4, 0)
	p2.Feed([]byte("id:z\n\n"))
	errLine := p2.Feed([]byte("data:abcd"))
	if !errors.Is(errLine, stream.ErrLineTooLong) || errors.Is(errLine, stream.ErrDataTooLong) {
		t.Fatal("error types must be distinguishable", errLine)
	}
	if p2.LastEventID() != "z" {
		t.Fatal("line-limit changed state", p2.LastEventID())
	}
}

func TestTrailingAndFinish(t *testing.T) {
	var got []stream.Event
	p := newP(&got, 0, 0)
	p.Feed([]byte("data:open"))
	if len(got) != 0 {
		t.Fatal("unterminated event auto-dispatched")
	}
	p.Finish()
	if len(got) != 1 || got[0].Data != "open" {
		t.Fatal("Finish must dispatch the open event", got)
	}
	p.Feed([]byte("data:next\n\n"))
	if len(got) != 2 {
		t.Fatal("parser must continue after Finish", got)
	}
}
