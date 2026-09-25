package stream_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/stream"
)

func hx(s ...byte) []byte { return s }

// runAll 以整段与给定切分序列逐种方式喂入，返回每次输出（应全部相同）。
func runAll(t *testing.T, cfg stream.Config, in []byte, cuts ...int) [][]byte {
	t.Helper()
	var outs [][]byte
	feed := func(sizes []int) []byte {
		tr := stream.New(cfg)
		pos := 0
		for _, sz := range sizes {
			if pos >= len(in) {
				break
			}
			end := pos + sz
			if end > len(in) {
				end = len(in)
			}
			if _, err := tr.Write(in[pos:end]); err != nil {
				t.Fatalf("write: %v", err)
			}
			pos = end
		}
		if pos < len(in) {
			if _, err := tr.Write(in[pos:]); err != nil {
				t.Fatalf("write tail: %v", err)
			}
		}
		if err := tr.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		return bytes.Clone(tr.Output())
	}
	outs = append(outs, feed([]int{len(in)}))
	for _, c := range cuts {
		var sz []int
		for i := 0; i < len(in); i += c {
			sz = append(sz, c)
		}
		outs = append(outs, feed(sz))
	}
	var ones []int
	for range in {
		ones = append(ones, 1)
	}
	outs = append(outs, feed(ones))
	return outs
}

func TestReplacementSamples(t *testing.T) {
	rep := []byte{0xEF, 0xBF, 0xBD}
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"f0-90-80-41", hx(0xF0, 0x90, 0x80, 0x41), bytes.Join([][]byte{rep, {0x41}}, nil)},
		{"e0-80-80", hx(0xE0, 0x80, 0x80), bytes.Repeat(rep, 3)},
		{"ed-a0-80", hx(0xED, 0xA0, 0x80), bytes.Repeat(rep, 3)},
		{"c0-af", hx(0xC0, 0xAF), bytes.Repeat(rep, 2)},
		{"f4-90-80-80", hx(0xF4, 0x90, 0x80, 0x80), bytes.Repeat(rep, 4)},
		{"e2-82-eof", hx(0xE2, 0x82), rep},
		{"80-80", hx(0x80, 0x80), bytes.Repeat(rep, 2)},
	}
	for _, tc := range cases {
		for _, out := range runAll(t, stream.Config{Dir: stream.U8ToU8}, tc.in, 1, 2, 3) {
			if !bytes.Equal(out, tc.want) {
				t.Fatalf("%s: got %x want %x", tc.name, out, tc.want)
			}
		}
	}
}

func TestStrictOffsetAndLength(t *testing.T) {
	cases := []struct {
		in          []byte
		off, length int64
	}{
		{hx(0x41, 0xC0, 0xAF), 1, 1},
		{hx(0xF0, 0x90, 0x80, 0x41), 0, 4},
		{hx(0xED, 0xA0, 0x80), 1, 2},
		{hx(0x80), 0, 1},
	}
	for _, tc := range cases {
		for _, chunk := range []int{1, 2, 3, 64} {
			tr := stream.New(stream.Config{Dir: stream.U8ToU8, Strict: true})
			var ue *stream.UnitError
			var err error
			for pos := 0; pos < len(tc.in); pos += chunk {
				end := min(pos+chunk, len(tc.in))
				_, err = tr.Write(tc.in[pos:end])
				if err != nil {
					break
				}
			}
			if err == nil {
				err = tr.Close()
			}
			if !errors.As(err, &ue) || !errors.Is(err, stream.ErrBadUnit) ||
				ue.Offset != tc.off || int64(ue.Len) != tc.length {
				t.Fatalf("chunk %d in %x: got %#v want off=%d len=%d", chunk, tc.in, err, tc.off, tc.length)
			}
			if _, e := tr.Write(tc.in); !errors.Is(e, err) {
				t.Fatalf("terminal: got %v want same", e)
			}
		}
	}
}

func TestTruncationDistinctAndEveryPoint(t *testing.T) {
	in := append([]byte("Aé中"), hx(0xF0, 0x9F, 0x98, 0x80)...) // 1+2+3+4
	for cut := range len(in) + 1 {
		tr := stream.New(stream.Config{Dir: stream.U8ToU8, Strict: true})
		_, _ = tr.Write(in[:cut])
		err := tr.Close()
		boundary := cut == 0 || cut == 1 || cut == 3 || cut == 6 || cut == 10
		if boundary && err != nil {
			t.Fatalf("cut %d expected clean close, got %v", cut, err)
		}
		if !boundary {
			var ue *stream.UnitError
			if !errors.As(err, &ue) || !errors.Is(err, stream.ErrTruncated) || errors.Is(err, stream.ErrBadUnit) {
				t.Fatalf("cut %d expected distinct truncation, got %v", cut, err)
			}
		}
	}
}

func TestConservationAndPendingCap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	in := make([]byte, 5000)
	rng.Read(in)
	tr := stream.New(stream.Config{Dir: stream.U8ToU8})
	maxPend := 0
	for i := 0; i < len(in); {
		n := 1 + rng.Intn(7)
		end := min(i+n, len(in))
		_, _ = tr.Write(in[i:end])
		if tr.PendingLen() > maxPend {
			maxPend = tr.PendingLen()
		}
		i = end
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if maxPend > stream.MaxPending {
		t.Fatalf("pending %d > cap %d", maxPend, stream.MaxPending)
	}
	s := tr.Stats()
	if s.InBytes+s.BadBytes+s.BOMBytes != int64(len(in)) {
		t.Fatalf("conservation: %d+%d+%d != %d", s.InBytes, s.BadBytes, s.BOMBytes, len(in))
	}
}
