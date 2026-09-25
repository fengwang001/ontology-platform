package api_test

import (
	"bytes"
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/esc"
)

var payloads = [][]byte{
	{},
	{0x41},
	{esc.Flag},
	{esc.Esc},
	{esc.Flag, esc.Esc, 0x00, 0xFF},
	bytes.Repeat([]byte{0x41, esc.Flag, esc.Esc}, 40),
}

func equalFrames(a, b [][]byte) bool { return slices.EqualFunc(a, b, bytes.Equal) }
func testStream() []byte {
	var s []byte
	for _, p := range payloads {
		s = append(s, api.Frame(p)...)
	}
	return s
}

func mustDecode(t *testing.T, f *api.Framer, chunks ...[]byte) {
	t.Helper()
	for _, c := range chunks {
		if _, err := f.Feed(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, p := range payloads {
		f := api.New()
		got, err := f.Feed(api.Frame(p))
		if err != nil || len(got) != 1 || !bytes.Equal(got[0], p) || f.Flush() != nil {
			t.Fatalf("payload %x: got %x err %v", p, got, err)
		}
	}
}

func TestSplitInvariance(t *testing.T) {
	stream := testStream()
	ref := api.New()
	mustDecode(t, ref, stream)
	want := ref.Frames()
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		var chunks [][]byte
		for pos := 0; pos < len(stream); {
			n := 1 + rng.Intn(len(stream)-pos)
			chunks = append(chunks, stream[pos:pos+n])
			pos += n
		}
		f := api.New()
		mustDecode(t, f, chunks...)
		if !equalFrames(f.Frames(), want) {
			t.Fatalf("trial %d: frames differ from one-shot", trial)
		}
	}
}

func TestFaults(t *testing.T) {
	cases := []struct {
		name   string
		feed   []byte // fed after one good frame is already collected
		flush  bool   // error comes from Flush, not Feed
		want   error
		resume []byte // valid continuation after rejection
		last   []byte // payload the continuation must complete
	}{
		{"bad escape", []byte{esc.Flag, esc.Esc, esc.Flag}, false, api.ErrBadEscape, []byte{esc.Flag, 0x41, esc.Flag}, []byte{0x41}},
		{"truncated escape", []byte{esc.Flag, esc.Esc}, true, api.ErrTruncatedEscape, []byte{esc.Map(esc.Flag), esc.Flag}, []byte{esc.Flag}},
		{"truncated frame", []byte{esc.Flag, 0x41}, true, api.ErrTruncatedFrame, []byte{0x42, esc.Flag}, []byte{0x41, 0x42}},
	}
	for _, c := range cases {
		f := api.New()
		mustDecode(t, f, api.Frame([]byte{0x99}))
		before := f.Frames()
		_, err := f.Feed(c.feed)
		if err == nil && c.flush {
			err = f.Flush()
		}
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if !equalFrames(f.Frames(), before) {
			t.Fatalf("%s: frames changed after rejection", c.name)
		}
		got, err := f.Feed(c.resume) // still usable, state preserved
		if err != nil || len(got) != 1 || !bytes.Equal(got[0], c.last) {
			t.Fatalf("%s: resume got %x err %v", c.name, got, err)
		}
	}
	if errors.Is(api.ErrBadEscape, api.ErrTruncatedEscape) ||
		errors.Is(api.ErrTruncatedEscape, api.ErrTruncatedFrame) ||
		errors.Is(api.ErrBadEscape, api.ErrTruncatedFrame) {
		t.Fatal("sentinel errors not distinct")
	}
}

func TestConcurrentFrames(t *testing.T) {
	f := api.New()
	mustDecode(t, f, testStream())
	want := f.Frames()
	const n = 16
	start := make(chan struct{})
	bad := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				if !equalFrames(f.Frames(), want) {
					bad[i] = true
					return
				}
			}
			if f.SelfCheck() != nil || api.Frame([]byte{0x1}) == nil {
				bad[i] = true
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if slices.Contains(bad, true) {
		t.Fatal("a goroutine saw inconsistent frames")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
