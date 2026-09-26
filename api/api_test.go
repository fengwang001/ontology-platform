package api

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/esc"
	"ontology/frame"
)

func TestPublicAPI(t *testing.T) {
	a := New()
	cases := [][][]byte{
		{},
		{nil, []byte("a\nb"), []byte("x\\y"), {}},
		{[]byte("\\\n"), []byte("")},
	}
	for _, fs := range cases {
		got, err := a.DecodeFrames(a.EncodeFrames(fs))
		if err != nil || len(got) != len(fs) {
			t.Fatalf("round trip %q: %v %v", fs, got, err)
		}
		for i := range fs {
			if !bytes.Equal(got[i], fs[i]) {
				t.Errorf("frame %d: %q != %q", i, got[i], fs[i])
			}
		}
	}
	if got := a.EncodeFrames(nil); got == nil || len(got) != 0 {
		t.Errorf("empty input must encode to empty non-nil stream, got %v", got)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestFailureSentinels(t *testing.T) {
	a := New()
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"illegal", []byte("a\\x\n"), esc.ErrIllegalEscape},
		{"dangling-delimiter", []byte("ab\\\n"), esc.ErrDanglingEscape},
		{"dangling-eof", []byte("ab\\"), esc.ErrDanglingEscape},
		{"missing-terminator", []byte("ab"), frame.ErrMissingTerminator},
	}
	for _, c := range cases {
		got, err := a.DecodeFrames(c.raw)
		if !errors.Is(err, c.want) || got != nil {
			t.Errorf("%s: frames=%v err=%v, want %v and nil frames", c.name, got, err, c.want)
		}
	}
	r := frame.NewReader([]byte("ok\nab\\"))
	if f, err := r.NextFrame(); err != nil || string(f) != "ok" {
		t.Fatalf("first frame: %q %v", f, err)
	}
	p := r.Pos()
	_, err := r.NextFrame()
	if !errors.Is(err, esc.ErrDanglingEscape) || r.Pos() != p {
		t.Fatalf("cursor moved=%d or err=%v", r.Pos(), err)
	}
	if _, err := r.NextFrame(); !errors.Is(err, esc.ErrDanglingEscape) {
		t.Fatalf("reader not reusable: %v", err)
	}
	if _, err := frame.NewReader(nil).NextFrame(); err != io.EOF {
		t.Fatalf("empty stream: %v want EOF", err)
	}
}

func TestConcurrentEncodeDecode(t *testing.T) {
	a := New()
	shared := a.EncodeFrames([][]byte{
		[]byte("same"), []byte("li\nne"), []byte("ba\\ck"), {}, []byte("\n\\"),
	})
	want, err := a.DecodeFrames(shared)
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	lists := make([][][]byte, n)
	serial := make([][]byte, n)
	for g := range lists {
		rng := rand.New(rand.NewSource(int64(g)))
		fs := make([][]byte, 1+rng.Intn(6))
		for i := range fs {
			b := make([]byte, rng.Intn(10))
			for j := range b {
				switch rng.Intn(4) {
				case 0:
					b[j] = '\\'
				case 1:
					b[j] = '\n'
				default:
					b[j] = byte('a' + rng.Intn(4))
				}
			}
			fs[i] = b
		}
		lists[g] = fs
		serial[g] = a.EncodeFrames(fs)
	}
	var decBad, encBad, checkBad atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			got, err := a.DecodeFrames(shared)
			if err != nil || len(got) != len(want) {
				decBad.Add(1)
				return
			}
			for i := range got {
				if !bytes.Equal(got[i], want[i]) {
					decBad.Add(1)
				}
			}
		}()
		go func(g int) {
			defer wg.Done()
			if !bytes.Equal(a.EncodeFrames(lists[g]), serial[g]) {
				encBad.Add(1)
			}
		}(g)
		go func() {
			defer wg.Done()
			if err := a.SelfCheck(); err != nil {
				checkBad.Add(1)
			}
		}()
	}
	wg.Wait()
	if decBad.Load() != 0 || encBad.Load() != 0 || checkBad.Load() != 0 {
		t.Fatalf("concurrent failures: decode=%d encode=%d selfcheck=%d",
			decBad.Load(), encBad.Load(), checkBad.Load())
	}
}
