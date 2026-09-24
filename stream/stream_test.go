package stream_test

import (
	"bytes"
	"errors"
	"io"
	"ontology/stream"
	"testing"
)

func run(t *testing.T, c stream.Config, in []byte, chunk int) ([]byte, stream.Stats, error) {
	t.Helper()
	x := stream.New(c)
	for len(in) > 0 {
		n := chunk
		if n > len(in) {
			n = len(in)
		}
		m, err := x.Write(in[:n])
		in = in[m:]
		if err != nil {
			return x.Output(), x.Stats(), err
		}
		if m == 0 {
			in = in[1:]
		}
	}
	return x.Output(), x.Stats(), x.Close()
}

func TestReplacementSamples(t *testing.T) {
	fffd := []byte{0xef, 0xbf, 0xbd}
	cases := []struct {
		name     string
		in, want []byte
	}{
		{"bad-third", []byte{0xf0, 0x90, 0x80, 0x41}, append(append([]byte{}, fffd...), 'A')},
		{"e0", []byte{0xe0, 0x80, 0x80}, bytes.Repeat(fffd, 3)},
		{"surrogate", []byte{0xed, 0xa0, 0x80}, bytes.Repeat(fffd, 3)},
		{"overlong", []byte{0xc0, 0xaf}, bytes.Repeat(fffd, 2)},
		{"too-high", []byte{0xf4, 0x90, 0x80, 0x80}, bytes.Repeat(fffd, 4)},
		{"eof-prefix", []byte{0xe2, 0x82}, fffd},
		{"continuation", []byte{0x80, 0x80}, bytes.Repeat(fffd, 2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range []int{1, 2, 3, 4, 64} {
				got, _, err := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, tc.in, n)
				if err != nil || !bytes.Equal(got, tc.want) {
					t.Fatalf("chunk %d: %v %q", n, err, got)
				}
			}
		})
	}
}

func TestSplitsStrictErrorsAndStats(t *testing.T) {
	in := []byte("A目😀")
	in = append(in, 0xf0, 0x90, 0x80, 0x41)
	ref, rs, err := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, in, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	var ue *stream.UnitError
	if !errors.As(err, &ue) || ue.Offset != 7 || ue.Length != 3 || !errors.Is(err, stream.ErrInvalid) {
		t.Fatal(err)
	}
	for n := 1; n <= len(in); n++ {
		got, st, e := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, in, n)
		if !bytes.Equal(got, ref) || !errors.Is(e, stream.ErrInvalid) {
			t.Fatal(n, got, e)
		}
		var g *stream.UnitError
		errors.As(e, &g)
		if g.Offset != ue.Offset || g.Length != ue.Length {
			t.Fatalf("n=%d %+v", n, g)
		}
		if st.Scalars != rs.Scalars || st.Invalid != rs.Invalid {
			t.Fatalf("stats n=%d", n)
		}
	}
}

func TestTruncationDistinctAndBoundaries(t *testing.T) {
	base := []byte("A目😀")
	for i := 0; i <= len(base); i++ {
		_, _, err := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8, Mode: stream.Strict}, base[:i], 64)
		wantTrunc := i == 2 || i == 4 || i == 5
		if wantTrunc && !errors.Is(err, stream.ErrTruncated) {
			t.Fatal(i, err)
		}
		if !wantTrunc && err != nil {
			t.Fatal(i, err)
		}
	}
}

func TestBOMRoundTripIdempotent(t *testing.T) {
	in := []byte{0xef, 0xbb, 0xbf, 'A', 0xef, 0xbb, 0xbf}
	got, _, err := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, in, 2)
	if err != nil || !bytes.Equal(got, []byte{'A', 0xef, 0xbb, 0xbf}) {
		t.Fatalf("%q %v", got, err)
	}
	again, _, _ := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, got, 3)
	if !bytes.Equal(again, got) {
		t.Fatalf("idempotent %q", again)
	}
}

func TestLimitResume(t *testing.T) {
	in := bytes.Repeat([]byte("😀"), 8)
	var all, got []byte
	for len(in) > 0 {
		x := stream.New(stream.Config{From: stream.UTF8, To: stream.UTF8, Limit: 20})
		n, err := x.Write(in)
		if !errors.Is(err, stream.ErrOutputLimit) {
			t.Fatal(err)
		}
		all = append(all, x.Output()...)
		in = in[n:]
		_ = got
	}
	ref, _, _ := run(t, stream.Config{From: stream.UTF8, To: stream.UTF8}, bytes.Repeat([]byte("😀"), 8), 64)
	if !bytes.Equal(all, ref) {
		t.Fatalf("%q", all)
	}
}

var _ io.Writer = (*stream.Transcoder)(nil)
