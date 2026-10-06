package framing

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func runAll(cfg Config, data []byte) []Event {
	p := NewParser(cfg)
	return p.Feed(data)
}

func runByteByByte(cfg Config, data []byte) []Event {
	p := NewParser(cfg)
	var evs []Event
	buf := make([]Event, 0, 4)
	for _, b := range data {
		buf = p.FeedInto(buf[:0], []byte{b})
		evs = append(evs, buf...)
	}
	return evs
}

func TestBasicFixedLength(t *testing.T) {
	data := []byte("POST /x HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello")
	evs := runAll(DefaultConfig(), data)
	want := []Event{
		{Kind: EventHeaderComplete, Method: "POST", Target: "/x", Version: "1.1", Mode: BodyFixedLength},
		{Kind: EventBody, N: 5},
		{Kind: EventRequestEnd},
	}
	assertEvents(t, evs, want)
	if evs2 := runByteByByte(DefaultConfig(), data); !eventsEqual(evs, evs2) {
		t.Fatalf("split mismatch:\n%v\n%v", evs, evs2)
	}
}

func TestNoBodyModes(t *testing.T) {
	cases := map[string]string{
		"1.1": "GET / HTTP/1.1\r\nHost: a\r\n\r\n",
		"1.0": "GET / HTTP/1.0\r\n\r\n",
	}
	for name, req := range cases {
		evs := runAll(DefaultConfig(), []byte(req))
		if len(evs) != 2 || evs[0].Mode != BodyNone || evs[1].Kind != EventRequestEnd {
			t.Fatalf("%s: %+v", name, evs)
		}
		if evs[0].Version != name {
			t.Fatalf("version: %s", evs[0].Version)
		}
	}
}

func TestChunkedHappyPath(t *testing.T) {
	data := []byte("POST /c HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n" +
		"1;ext=v\r\na\r\n" +
		"0\r\n" +
		"X-Trailer: v\r\n\r\n")
	evs := runAll(DefaultConfig(), data)
	if evs[0].Mode != BodyChunked {
		t.Fatalf("mode %v", evs[0].Mode)
	}
	var body int
	for _, e := range evs {
		if e.Kind == EventBody {
			body += e.N
		}
	}
	if body != 6 {
		t.Fatalf("body=%d", body)
	}
	if evs[len(evs)-1].Kind != EventRequestEnd {
		t.Fatalf("last %v", evs[len(evs)-1])
	}
	if evs2 := runByteByByte(DefaultConfig(), data); !eventsEqual(evs, evs2) {
		t.Fatalf("split mismatch:\n%v\n%v", evs, evs2)
	}
}

func TestImmediateRejectPriority(t *testing.T) {
	// Header limit boundary: MaxHeaderBytes == n means the n-th byte is
	// accepted and the (n+1)-th rejects with header_too_large.
	limit := len("POST / HTTP/1.1\r\nX: 1\r\n")
	base := []byte("POST / HTTP/1.1\r\nX: 1\r\n")
	cfg := Config{MaxHeaderBytes: limit, MaxBodyBytes: 100}
	if evs := runAll(cfg, base); len(evs) != 0 {
		t.Fatalf("exactly at limit must still be accepted as a prefix: %v", evs)
	}
	if evs := runAll(cfg, append(append([]byte{}, base...), 'x')); lastReason(evs) != RejectHeaderTooLarge {
		t.Fatalf("one byte past limit: %v", evs)
	}
	// Exactly at the boundary is still accepted as a (syntactically open)
	// prefix; a complete header of exactly that many bytes must succeed.
	full := []byte("POST / HTTP/1.1\r\nX: 1\r\n\r\n")
	cfg2 := Config{MaxHeaderBytes: len(full), MaxBodyBytes: 100}
	evs := runAll(cfg2, full)
	if len(evs) != 2 || evs[1].Kind != EventRequestEnd {
		t.Fatalf("boundary: %v", evs)
	}

	// Over-size vs NUL on the same byte: over-size wins.
	buf := bytes.Repeat([]byte{'a'}, limit)
	buf = append(buf, 0)
	if r := lastReason(runAll(cfg, buf)); r != RejectHeaderTooLarge {
		t.Fatalf("priority oversize vs nul: %v", r)
	}

	// NUL vs bare LF on the same byte (a NUL is never LF), so test NUL
	// before a later bare LF: NUL appears first in the stream.
	data := []byte("POST / HTTP/1.1\r\nX: \x00\n")
	if r := lastReason(runAll(DefaultConfig(), data)); r != RejectZeroByte {
		t.Fatalf("nul: %v", r)
	}
	// Bare LF.
	data = []byte("POST / HTTP/1.1\n")
	if r := lastReason(runAll(DefaultConfig(), data)); r != RejectBareLF {
		t.Fatalf("bare lf: %v", r)
	}
	// Bare LF immediately after CR (the LF itself is valid) but CR then a
	// non-LF is fine.
}

func TestHeaderValidationOrder(t *testing.T) {
	cfg := Config{MaxHeaderBytes: 4096, MaxBodyBytes: 10}
	cases := []struct {
		name   string
		req    string
		reason RejectReason
	}{
		{"bad request line", "POST /x extra / HTTP/1.1\r\n\r\n", RejectSyntax},
		{"bad version", "GET / HTTP/2.0\r\n\r\n", RejectSyntax},
		{"empty header name", "GET / HTTP/1.1\r\n: v\r\n\r\n", RejectSyntax},
		{"whitespace before colon", "GET / HTTP/1.1\r\nX : v\r\n\r\n", RejectSyntax},
		{"folding line", "GET / HTTP/1.1\r\nX: v\r\n folded\r\n\r\n", RejectSyntax},
		{"cl and te", "POST / HTTP/1.1\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n", RejectLengthAndTransferEncoding},
		{"bad cl", "POST / HTTP/1.1\r\nContent-Length: abc\r\n\r\n", RejectContentLengthInvalid},
		{"empty cl", "POST / HTTP/1.1\r\nContent-Length: \r\n\r\n", RejectContentLengthInvalid},
		{"cl too large", "POST / HTTP/1.1\r\nContent-Length: 11\r\n\r\n", RejectLengthTooLarge},
		{"te gzip", "POST / HTTP/1.1\r\nTransfer-Encoding: gzip\r\n\r\n", RejectTransferEncodingUnsupported},
		{"te chunked not last", "POST / HTTP/1.1\r\nTransfer-Encoding: chunked, gzip\r\n\r\n", RejectTransferEncodingUnsupported},
		{"two te headers", "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n", RejectTransferEncodingUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if r := lastReason(runAll(cfg, []byte(tc.req))); r != tc.reason {
				t.Fatalf("got %v want %v", r, tc.reason)
			}
		})
	}
}

func TestDuplicateContentLength(t *testing.T) {
	cfg := Config{MaxHeaderBytes: 4096, MaxBodyBytes: 100}
	same := "POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 5\r\n\r\nhello"
	evs := runAll(cfg, []byte(same))
	if evs[0].Mode != BodyFixedLength || evs[len(evs)-1].Kind != EventRequestEnd {
		t.Fatalf("same: %v", evs)
	}
	diff := "POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 6\r\n\r\n"
	if r := lastReason(runAll(cfg, []byte(diff))); r != RejectContentLengthInvalid {
		t.Fatalf("diff: %v", r)
	}
	// Surrounding whitespace is ignored; otherwise comparison is literal.
	ws := "POST / HTTP/1.1\r\nContent-Length:  05 \r\nContent-Length: 5\r\n\r\nhello"
	if r := lastReason(runAll(cfg, []byte(ws))); r != RejectContentLengthInvalid {
		t.Fatalf("literal compare 05 vs 5: %v", r)
	}
}

func TestChunkErrors(t *testing.T) {
	cfg := Config{MaxHeaderBytes: 4096, MaxBodyBytes: 100}
	cases := []struct {
		name   string
		body   string
		reason RejectReason
	}{
		{"bad size hex", "5z\r\n", RejectChunkFormatInvalid},
		{"bare lf size", "5\n", RejectChunkFormatInvalid},
		{"size exceeds 64bit", "10000000000000000\r\n", RejectChunkFormatInvalid},
		{"chunk total too large", "65\r\n", RejectLengthTooLarge},
		{"data crlf bad", "2\r\naab\r\n", RejectChunkFormatInvalid},
		{"trailer bad syntax", "0\r\nbadline\r\n\r\n", RejectChunkFormatInvalid},
		{"trailer cl", "0\r\nContent-Length: 1\r\n\r\n", RejectTrailerInvalid},
		{"trailer te", "0\r\nTransfer-Encoding: chunked\r\n\r\n", RejectTrailerInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n"), tc.body...)
			evs := runAll(cfg, data)
			if r := lastReason(evs); r != tc.reason {
				t.Fatalf("got %v want %v; evs=%v", r, tc.reason, evs)
			}
			if r := lastReason(runByteByByte(cfg, data)); r != tc.reason {
				t.Fatalf("byte-by-byte got %v", r)
			}
		})
	}
}

func TestBodyBoundary(t *testing.T) {
	cfg := Config{MaxHeaderBytes: 4096, MaxBodyBytes: 5}
	ok := "POST / HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello"
	if r := lastReason(runAll(cfg, []byte(ok))); r != RejectNone {
		t.Fatalf("boundary: %v", r)
	}
	big := "POST / HTTP/1.1\r\nContent-Length: 6\r\n\r\nhelloo"
	if r := lastReason(runAll(cfg, []byte(big))); r != RejectLengthTooLarge {
		t.Fatalf("over: %v", r)
	}
}

func TestPipelining(t *testing.T) {
	data := []byte("POST /a HTTP/1.1\r\nContent-Length: 2\r\n\r\nhi" +
		"GET /b HTTP/1.1\r\n\r\n")
	evs := runAll(DefaultConfig(), data)
	var reqs int
	for _, e := range evs {
		if e.Kind == EventHeaderComplete {
			reqs++
		}
	}
	if reqs != 2 {
		t.Fatalf("reqs=%d evs=%v", reqs, evs)
	}
	if evs[3].Kind != EventHeaderComplete || evs[3].Target != "/b" {
		t.Fatalf("second request start: %v", evs[3])
	}
	if evs2 := runByteByByte(DefaultConfig(), data); !eventsEqual(evs, evs2) {
		t.Fatalf("pipeline split mismatch:\n%v\n%v", evs, evs2)
	}
}

func TestRejectClosesConnection(t *testing.T) {
	p := NewParser(DefaultConfig())
	first := p.Feed([]byte("GET / HTTP/1.1\n"))
	if lastReason(first) != RejectBareLF {
		t.Fatalf("first: %v", first)
	}
	more := p.Feed([]byte("GET / HTTP/1.1\r\n\r\n"))
	if len(more) != 0 {
		t.Fatalf("post-reject events: %v", more)
	}
	if !p.Closed() {
		t.Fatal("closed")
	}
}

func lastReason(evs []Event) RejectReason {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == EventReject {
			return evs[i].Reason
		}
	}
	return RejectNone
}

func eventsEqual(a, b []Event) bool {
	a = mergeBodyEvents(a)
	b = mergeBodyEvents(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mergeBodyEvents merges adjacent body events. The specification only
// requires equality after such a merge, since chunk boundaries may split
// body bytes arbitrarily.
func mergeBodyEvents(evs []Event) []Event {
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		if e.Kind == EventBody && len(out) > 0 && out[len(out)-1].Kind == EventBody {
			out[len(out)-1].N += e.N
			continue
		}
		out = append(out, e)
	}
	return out
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if !eventsEqual(got, want) {
		t.Fatalf("got=%v\nwant=%v", got, want)
	}
}

func TestReasonsDistinguishable(t *testing.T) {
	seen := map[string]bool{}
	for r := RejectHeaderTooLarge; r <= RejectTrailerInvalid; r++ {
		s := r.String()
		if seen[s] || strings.Contains(s, "invalid-reason") {
			t.Fatalf("reason collision %s", s)
		}
		seen[s] = true
	}
	if fmt.Sprint(RejectHeaderTooLarge) != "header_too_large" {
		t.Fatal("Stringer not wired")
	}
}
