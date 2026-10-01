package ontology

import (
	"sync"
	"testing"
)

func TestInvalidUTF8RejectsWholeChunk(t *testing.T) {
	tz := NewTokenizer()
	before, _ := tz.Feed([]byte("hello "))
	statsBefore := tz.Stats()
	if len(before) != 1 || before[0].Text != "hello" {
		t.Fatalf("setup %+v", before)
	}

	badCases := [][]byte{
		{0xff},
		{0xc0, 0xaf},             // overlong encoding of '/'
		{0xe0, 0x80, 0xaf},       // overlong 3-byte
		{0xed, 0xa0, 0x80},       // surrogate U+D800
		{0xf4, 0x90, 0x80, 0x80}, // U+110000
		{0x80},                   // stray continuation
		{'x', 0xfe},
		{0xc3, 0x2f}, // declared 2-byte, '/' not a continuation
	}
	for _, bad := range badCases {
		out, err := tz.Feed(bad)
		if err == nil || err.(*FeedError).Kind != ErrInvalidUTF8 {
			t.Fatalf("bad % x: want ErrInvalidUTF8, got out=%v err=%v", bad, out, err)
		}
		if out != nil {
			t.Fatalf("bad % x: rejected Feed must return no tokens", bad)
		}
		if tz.Stats() != statsBefore {
			t.Fatalf("bad % x: stats changed by rejected Feed: before=%+v after=%+v",
				bad, statsBefore, tz.Stats())
		}
	}

	out, err := tz.Feed([]byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := tz.Close()
	if err != nil {
		t.Fatal(err)
	}
	got = append(out, got...)
	if len(got) != 1 || got[0].Text != "world" || got[0].Start != 6 {
		t.Fatalf("post-reject tokens %+v", got)
	}

	// Invalid byte after a buffered incomplete tail rejects the new chunk
	// but leaves the tail buffered, so a following good chunk recovers.
	tz2 := NewTokenizer()
	if _, err := tz2.Feed([]byte{0xc3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tz2.Feed([]byte{0x2f}); err == nil {
		t.Fatal("invalid continuation expected to reject the chunk")
	}
	out, err = tz2.Feed([]byte{0xa9})
	if err != nil {
		t.Fatalf("recovery feed: %v", err)
	}
	// 'é' bytes are separators: the token "é" never existed; the token
	// pending before the rejected chunk ("") was empty. Nothing to flush,
	// and Close must succeed now that the tail is complete.
	if len(out) != 0 {
		t.Fatalf("recovery output %+v", out)
	}
	if _, err := tz2.Close(); err != nil {
		t.Fatalf("close after recovery: %v", err)
	}
}

func TestTruncatedTailThenRecover(t *testing.T) {
	tz := NewTokenizer()
	if _, err := tz.Feed([]byte("caf")); err != nil {
		t.Fatal(err)
	}
	if _, err := tz.Feed([]byte{0xc3}); err != nil {
		t.Fatal(err)
	}
	statsMid := tz.Stats()

	if _, err := tz.Close(); err == nil || err.(*FeedError).Kind != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if tz.Stats() != statsMid {
		t.Fatalf("stats changed after rejected close: %+v vs %+v", tz.Stats(), statsMid)
	}

	out, err := tz.Feed([]byte{0xa9})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tz.Close()
	if err != nil {
		t.Fatal(err)
	}
	got = append(out, got...)
	// 'é' is a non-ASCII separator, so the token "caf" is emitted in the
	// recovery Feed; Close has nothing left.
	if len(got) != 1 || got[0].Text != "caf" || got[0].Start != 0 || got[0].End != 3 {
		t.Fatalf("recovered token %+v", got)
	}

	if _, err := tz.Close(); err == nil || err.(*FeedError).Kind != ErrClosed {
		t.Fatalf("want ErrClosed on second close, got %v", err)
	}
	if _, err := tz.Feed([]byte("x")); err == nil || err.(*FeedError).Kind != ErrClosed {
		t.Fatalf("want ErrClosed on feed after close, got %v", err)
	}

	// A 3-byte rune arriving piece by piece; Close is rejected twice.
	tz2 := NewTokenizer()
	fb := []byte("ﬁ") // 0xEF 0xAC 0x81
	if len(fb) != 3 {
		t.Fatalf("ligature must be 3 bytes, got %d", len(fb))
	}
	if _, err := tz2.Feed(fb[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tz2.Close(); err == nil {
		t.Fatal("truncated after 1 byte")
	}
	if _, err := tz2.Feed(fb[1:2]); err != nil {
		t.Fatal(err)
	}
	if _, err := tz2.Close(); err == nil {
		t.Fatal("truncated after 2 bytes")
	}
	if _, err := tz2.Feed(fb[2:]); err != nil {
		t.Fatal(err)
	}
	got, err = tz2.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "fi" || got[0].Start != 0 || got[0].End != 3 {
		t.Fatalf("3-byte recovery %+v", got)
	}
}

func splitChunks(s string, size int) [][]byte {
	raw := []byte(s)
	var out [][]byte
	for i := 0; i < len(raw); i += size {
		end := i + size
		if end > len(raw) {
			end = len(raw)
		}
		out = append(out, raw[i:end])
	}
	return out
}

func TestConcurrentFeedClose(t *testing.T) {
	// One writer feeds a deterministic stream while readers concurrently
	// call Stats; the final result must equal the naive one-pass result.
	input := "Straße find œuf, ÆTHER abc\u0304!! 42 café"
	chunks := splitChunks(input, 7)

	tz := NewTokenizer()
	start := make(chan struct{})
	var readers sync.WaitGroup
	for g := 0; g < 8; g++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for i := 0; i < 300; i++ {
				_ = tz.Stats()
			}
		}()
	}
	close(start)

	var got []Token
	for _, c := range chunks {
		out, err := tz.Feed(c)
		if err != nil {
			t.Fatalf("feed: %v", err)
		}
		got = append(got, out...)
	}
	readers.Wait()

	fin, err := tz.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	got = append(got, fin...)
	checkSame(t, "concurrent", input, got, tz.Stats())

	// After close, concurrent Feed/Close all get ErrClosed.
	var hammer sync.WaitGroup
	for i := 0; i < 16; i++ {
		hammer.Add(2)
		go func() {
			defer hammer.Done()
			if _, err := tz.Feed([]byte("y")); err == nil || err.(*FeedError).Kind != ErrClosed {
				t.Errorf("post-close feed: %v", err)
			}
		}()
		go func() {
			defer hammer.Done()
			if _, err := tz.Close(); err == nil || err.(*FeedError).Kind != ErrClosed {
				t.Errorf("second close: %v", err)
			}
		}()
	}
	hammer.Wait()
}
