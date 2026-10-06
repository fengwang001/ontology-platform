package framer

import (
	"sync"
	"testing"
)

type wantEvent struct {
	kind    EventKind
	mode    BodyMode
	bodyLen uint64
	err     ErrorKind
}

func feedInChunks(t *testing.T, input []byte, limits Limits, chunkSize int) []Event {
	t.Helper()
	decoder := NewDecoder(limits)
	var got []Event
	for start := 0; start < len(input); {
		end := start + chunkSize
		if end > len(input) {
			end = len(input)
		}
		got = append(got, decoder.Push(input[start:end])...)
		start = end
	}
	return got
}

func assertEvents(t *testing.T, got []Event, want []wantEvent) {
	t.Helper()
	got = mergeBodyEvents(got)
	if len(got) != len(want) {
		t.Fatalf("event count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Kind != want[i].kind || got[i].Mode != want[i].mode ||
			got[i].BodyLen != want[i].bodyLen || got[i].Error != want[i].err {
			t.Fatalf("event %d = %+v, want %+v; all got %v", i, got[i], want[i], got)
		}
	}
}

func mergeBodyEvents(events []Event) []Event {
	merged := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Kind == EventBody && len(merged) > 0 && merged[len(merged)-1].Kind == EventBody {
			merged[len(merged)-1].BodyLen += event.BodyLen
			continue
		}
		merged = append(merged, event)
	}
	return merged
}

func TestExplicitDecoderCases(t *testing.T) {
	const completeHead = "POST /x HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc"
	tests := []struct {
		name   string
		input  string
		limits Limits
		want   []wantEvent
	}{
		{
			name:   "header limit boundary accepted",
			input:  completeHead,
			limits: Limits{MaxHeaderBytes: 39, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeFixed},
				{kind: EventBody, bodyLen: 3},
				{kind: EventEnd},
			},
		},
		{
			name:   "header limit one byte short rejected",
			input:  completeHead,
			limits: Limits{MaxHeaderBytes: 38, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventReject, err: ErrorHeaderTooLarge},
			},
		},
		{
			name:   "null precedes bare line feed",
			input:  "GET / HTTP/1.1\r\nX: a\n\x00",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorBareLineFeed}},
		},
		{
			name:   "oversize precedes null",
			input:  "\x00",
			limits: Limits{MaxHeaderBytes: 0, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorHeaderTooLarge}},
		},
		{
			name:   "syntax precedes conflicting framing",
			input:  "GET / HTTP/9\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorSyntax}},
		},
		{
			name:   "conflict precedes invalid length",
			input:  "GET / HTTP/1.1\r\nContent-Length: x\r\nTransfer-Encoding: chunked\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorLengthAndTransferEncoding}},
		},
		{
			name:   "invalid length precedes too large",
			input:  "GET / HTTP/1.1\r\nContent-Length: 11x\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorInvalidContentLength}},
		},
		{
			name:   "duplicate identical length accepted",
			input:  "POST / HTTP/1.1\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\nhi",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeFixed},
				{kind: EventBody, bodyLen: 2},
				{kind: EventEnd},
			},
		},
		{
			name:   "duplicate differing length rejected",
			input:  "POST / HTTP/1.1\r\nContent-Length: 2\r\nContent-Length: 3\r\n\r\nhi",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorInvalidContentLength}},
		},
		{
			name:   "body boundary accepted",
			input:  "POST / HTTP/1.1\r\nContent-Length: 10\r\n\r\n0123456789",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeFixed},
				{kind: EventBody, bodyLen: 10},
				{kind: EventEnd},
			},
		},
		{
			name:   "body boundary one over rejected",
			input:  "POST / HTTP/1.1\r\nContent-Length: 11\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorBodyTooLarge}},
		},
		{
			name:   "multiple transfer encoding headers unsupported",
			input:  "GET / HTTP/1.1\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want:   []wantEvent{{kind: EventReject, err: ErrorUnsupportedTransferEncoding}},
		},
		{
			name:   "chunked request",
			input:  "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeChunked},
				{kind: EventBody, bodyLen: 3},
				{kind: EventEnd},
			},
		},
		{
			name:   "chunk data final byte carriage return",
			input:  "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n\r\r\n0\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeChunked},
				{kind: EventBody, bodyLen: 1},
				{kind: EventEnd},
			},
		},
		{
			name:   "chunk size over 64 bits",
			input:  "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n10000000000000000\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeChunked},
				{kind: EventReject, err: ErrorChunkFormat},
			},
		},
		{
			name:   "invalid chunk terminator",
			input:  "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabcX",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeChunked},
				{kind: EventBody, bodyLen: 3},
				{kind: EventReject, err: ErrorChunkFormat},
			},
		},
		{
			name:   "forbidden trailer",
			input:  "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nContent-Length: 0\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeChunked},
				{kind: EventReject, err: ErrorTrailer},
			},
		},
		{
			name:   "pipeline starts immediately",
			input:  "POST / HTTP/1.1\r\nContent-Length: 1\r\n\r\naGET / HTTP/1.1\r\n\r\n",
			limits: Limits{MaxHeaderBytes: 1000, MaxBodyBytes: 10},
			want: []wantEvent{
				{kind: EventHeaders, mode: BodyModeFixed},
				{kind: EventBody, bodyLen: 1},
				{kind: EventEnd},
				{kind: EventHeaders, mode: BodyModeNone},
				{kind: EventEnd},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/byte", func(t *testing.T) {
			assertEvents(t, feedInChunks(t, []byte(tc.input), tc.limits, 1), tc.want)
		})
		t.Run(tc.name+"/all", func(t *testing.T) {
			assertEvents(t, feedInChunks(t, []byte(tc.input), tc.limits, len(tc.input)+1), tc.want)
		})
	}
}

func TestBytesAfterRejectionAreIgnored(t *testing.T) {
	decoder := NewDecoder(Limits{MaxHeaderBytes: 5, MaxBodyBytes: 10})
	first := decoder.Push([]byte("GET / HTTP/1.1\r\n\r\nGET / HTTP/1.1\r\n\r\n"))
	if len(first) != 1 || first[0].Kind != EventReject || first[0].Error != ErrorHeaderTooLarge {
		t.Fatalf("first = %v", first)
	}
	second := decoder.Push([]byte("POST / HTTP/1.1\r\nContent-Length: 0\r\n\r\n"))
	if len(second) != 0 || !decoder.Closed() {
		t.Fatalf("second = %v, closed = %v", second, decoder.Closed())
	}
}

func TestConcurrentConnectionsAndSerialWrites(t *testing.T) {
	input := []byte("POST / HTTP/1.1\r\nContent-Length: 4\r\n\r\nabcdGET / HTTP/1.1\r\n\r\n")
	limits := Limits{MaxHeaderBytes: 300, MaxBodyBytes: 10}
	want := []wantEvent{
		{kind: EventHeaders, mode: BodyModeFixed},
		{kind: EventBody, bodyLen: 4},
		{kind: EventEnd},
		{kind: EventHeaders, mode: BodyModeNone},
		{kind: EventEnd},
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decoder := NewDecoder(limits)
			events := mergeBodyEvents(decoder.Push(input))
			assertEvents(t, events, want)
		}()
	}

	shared := NewDecoder(limits)
	var mu sync.Mutex
	var sharedEvents []Event
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			sharedEvents = append(sharedEvents, shared.Push(input[:len(input)/2])...)
			sharedEvents = append(sharedEvents, shared.Push(input[len(input)/2:])...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sharedEvents = mergeBodyEvents(sharedEvents)
	if shared.Closed() || len(sharedEvents) != 16*len(want) {
		t.Fatalf("shared events = %d, closed = %v, events = %v", len(sharedEvents), shared.Closed(), sharedEvents)
	}
}

func TestLargeFixedBodyDoesNotBuffer(t *testing.T) {
	const bodySize = 4 << 20
	decoder := NewDecoder(Limits{MaxHeaderBytes: 300, MaxBodyBytes: bodySize})
	header := []byte("POST /large HTTP/1.1\r\nContent-Length: 4194304\r\n\r\n")
	events := mergeBodyEvents(decoder.Push(header))
	if len(events) != 1 || events[0].Kind != EventHeaders || events[0].Mode != BodyModeFixed {
		t.Fatalf("header events = %v", events)
	}

	zero := make([]byte, 1024)
	var total uint64
	for total < bodySize {
		got := decoder.Push(zero)
		for _, event := range got {
			if event.Kind == EventBody {
				total += event.BodyLen
			}
		}
	}
	if total != bodySize || decoder.Closed() {
		t.Fatalf("total = %d, closed = %v", total, decoder.Closed())
	}
}

func BenchmarkLargeFixedBodyChunk(b *testing.B) {
	const bodySize = 1 << 20
	decoder := NewDecoder(Limits{MaxHeaderBytes: 300, MaxBodyBytes: bodySize})
	decoder.Push([]byte("POST /large HTTP/1.1\r\nContent-Length: 1048576\r\n\r\n"))
	chunk := make([]byte, 4096)
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decoder.Push(chunk)
		if decoder.remaining == 0 {
			b.StopTimer()
			decoder.resetForNextRequest()
			decoder.state = stateFixedBody
			decoder.remaining = bodySize
			b.StartTimer()
		}
	}
}
