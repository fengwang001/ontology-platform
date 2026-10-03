package par

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/stream"
)

func single(src []byte, cfg stream.Config) ([]byte, stream.Stats, error) {
	t := stream.New(cfg)
	if _, err := t.Write(src); err == nil {
		err = t.Close()
		return t.Output(), t.Stats(), err
	} else {
		return t.Output(), t.Stats(), err
	}
}

var inputs = [][]byte{
	[]byte("aé€😀b"),
	{0xC3, 0x80, 0x80},
	{0xF0, 0x90, 0x80, 0x41},
	{0xE0, 0x80, 0x80, 0xED, 0xA0, 0x80},
	{0xEF, 0xBB, 0xBF, 0x61, 0xEF, 0xBB, 0xBF},
	{0xF4, 0x90, 0x80, 0x80, 0xC0, 0xAF},
	{},
}

func TestParAllK(t *testing.T) {
	for _, in := range inputs {
		want, ws, _ := single(in, stream.Config{})
		for k := 1; k <= 8; k++ {
			got, gs, err := Transcode(in, k, stream.Config{})
			if err != nil || !bytes.Equal(got, want) || gs != ws {
				t.Fatalf("%X k=%d: got %X %+v %v", in, k, got, gs, err)
			}
		}
	}
}

func TestParAllCutPoints(t *testing.T) {
	for _, in := range inputs {
		want, ws, _ := single(in, stream.Config{})
		for cut := 0; cut <= len(in); cut++ {
			s := align(in, cut)
			got, gs, err := run(in, []int{0, s, len(in)}, stream.Config{})
			if err != nil || !bytes.Equal(got, want) || gs != ws {
				t.Fatalf("%X cut %d (aligned %d): got %X %+v", in, cut, s, got, gs)
			}
		}
	}
}

func TestParStrict(t *testing.T) {
	in := append([]byte("abé€😀cd"), 0xF0, 0x90, 0x80, 0x41)
	in = append(in, []byte("xy")...)
	_, _, wantErr := single(in, stream.Config{Strict: true})
	for k := 1; k <= 8; k++ {
		_, _, err := Transcode(in, k, stream.Config{Strict: true})
		var le, we *stream.Error
		if !errors.As(err, &le) || !errors.As(wantErr, &we) || le.Off != we.Off || le.Len != we.Len {
			t.Fatalf("k=%d: got %v want %v", k, err, wantErr)
		}
	}
}

func TestParDeterministic(t *testing.T) {
	in := make([]byte, 4096)
	rand.New(rand.NewSource(7)).Read(in)
	want, _, _ := Transcode(in, 8, stream.Config{})
	for i := 0; i < 50; i++ {
		if got, _, _ := Transcode(in, 8, stream.Config{}); !bytes.Equal(got, want) {
			t.Fatal("nondeterministic output")
		}
	}
}

func TestParChecks(t *testing.T) {
	in := make([]byte, 1<<20)
	rand.New(rand.NewSource(9)).Read(in)
	before := Checks()
	if _, _, err := Transcode(in, 8, stream.Config{}); err != nil {
		t.Fatal(err)
	}
	got := Checks() - before
	if limit := int64(len(in)) + 8*16; got > limit {
		t.Fatalf("checks %d > limit %d", got, limit)
	}
	t.Logf("checks=%d input=%d", got, len(in))
}
