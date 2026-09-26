package frame

import (
	"bytes"
	"errors"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/esc"
)

// naiveEncode 是教科书参照：逐字节 `\`→`\\`、`\n`→`\n`，帧间插 `\n`。
func naiveEncode(frames [][]byte) []byte {
	r := strings.NewReplacer("\\", "\\\\", "\n", "\\n")
	var out []byte
	for _, f := range frames {
		out = append(out, r.Replace(string(f))...)
		out = append(out, '\n')
	}
	return out
}

func randFrames(rng *rand.Rand, m int) [][]byte {
	frames := make([][]byte, m)
	for i := range frames {
		p := make([]byte, rng.Intn(40))
		for j := range p {
			if r := rng.Intn(4); r < 2 {
				p[j] = []byte{'\\', '\n'}[r]
			} else {
				p[j] = byte(rng.Intn(256))
			}
		}
		frames[i] = p
	}
	return frames
}

func equalFrames(a, b [][]byte) bool {
	return slices.EqualFunc(a, b, bytes.Equal)
}

func TestRoundTrip(t *testing.T) {
	fixed := [][]byte{nil, {}, []byte("hi"), []byte("a\nb"), []byte(`x\y`), []byte(`\n\`)}
	if got, err := Decode(Encode(fixed)); err != nil || !equalFrames(got, fixed) {
		t.Fatalf("fixed round trip: got %q err %v", got, err)
	}
	rng := rand.New(rand.NewSource(2))
	for _, m := range []int{0, 1, 3, 100} {
		frames := randFrames(rng, m)
		if got, err := Decode(Encode(frames)); err != nil || !equalFrames(got, frames) {
			t.Fatalf("m=%d round trip failed: err %v", m, err)
		}
	}
}

func TestEncodeMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		frames := randFrames(rng, rng.Intn(20))
		if got, want := Encode(frames), naiveEncode(frames); !bytes.Equal(got, want) {
			t.Fatalf("Encode != naive: %x vs %x", got, want)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want error
	}{{"invalid escape", []byte("a\\xb\n"), esc.ErrInvalidEscape},
		{"dangling before delim", []byte("ab\\\n"), esc.ErrDanglingEscape},
		{"dangling at end", []byte("ab\\"), esc.ErrDanglingEscape},
		{"missing terminator", []byte("ab"), ErrMissingTerminator},
	}
	for _, c := range cases {
		got, err := Decode(c.in)
		if !errors.Is(err, c.want) || got != nil {
			t.Errorf("%s: got %q err %v", c.name, got, err)
		}
	}
	if errors.Is(esc.ErrInvalidEscape, esc.ErrDanglingEscape) ||
		errors.Is(esc.ErrDanglingEscape, ErrMissingTerminator) ||
		errors.Is(ErrMissingTerminator, esc.ErrInvalidEscape) {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestReaderCursorOnError(t *testing.T) {
	r := NewReader([]byte("ok\n\\x\n"))
	f, err := r.NextFrame()
	if err != nil || string(f) != "ok" || r.Pos() != 3 {
		t.Fatalf("first frame: %q pos %d err %v", f, r.Pos(), err)
	}
	if _, err := r.NextFrame(); !errors.Is(err, esc.ErrInvalidEscape) || r.Pos() != 3 {
		t.Fatalf("cursor moved or wrong error: pos %d err %v", r.Pos(), err)
	}
}

func TestSinglePass(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for _, m := range []int{100, 1000, 10000} {
		stream := Encode(randFrames(rng, m))
		if _, err := Decode(stream); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if lastChecked.Load() != int64(len(stream)) || lastLookback.Load() != 0 {
			t.Fatalf("m=%d: checked %d lookback %d, stream len %d",
				m, lastChecked.Load(), lastLookback.Load(), len(stream))
		}
	}
}

func TestConcurrent(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	shared := Encode(randFrames(rng, 500))
	want, _ := Decode(shared) // Encode 产物必然可解码
	lists := make([][][]byte, 8)
	encWant := make([][]byte, 8)
	for i := range lists {
		lists[i] = randFrames(rng, 20)
		encWant[i] = Encode(lists[i])
	}
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for g := 0; g < 8; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if got, err := Decode(shared); err != nil || !equalFrames(got, want) {
				errs <- "decode mismatch"
			}
		}()
		go func(i int) {
			defer wg.Done()
			if !bytes.Equal(Encode(lists[i]), encWant[i]) {
				errs <- "encode mismatch"
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
