package cobs

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveEncode is a deliberately naive encoder written directly from the
// block rules, used as an independent reference for round-trip tests.
func naiveEncode(data []byte) []byte {
	var out []byte
	i := 0
	for {
		j := i
		for j < len(data) && j-i < maxBlockData && data[j] != 0 {
			j++
		}
		run := j - i
		if run == maxBlockData {
			out = append(out, 0xFF)
			out = append(out, data[i:j]...)
			i = j
			continue
		}
		out = append(out, byte(run+1))
		out = append(out, data[i:j]...)
		i = j
		if i == len(data) {
			break
		}
		i++ // consume the zero that terminated the run
		if i == len(data) {
			out = append(out, 0x01)
			break
		}
	}
	return out
}

// naiveDecode decodes one frame by the block rules only, without any
// canonical-form check.
func naiveDecode(frame []byte) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(frame) {
		c := int(frame[i])
		i++
		n := c - 1
		if i+n > len(frame) {
			return nil, fmt.Errorf("truncated: code %d needs %d bytes, %d left", c, n, len(frame)-i)
		}
		for _, b := range frame[i : i+n] {
			if b == 0 {
				return nil, fmt.Errorf("zero byte inside block")
			}
		}
		out = append(out, frame[i:i+n]...)
		i += n
		if c < 0xFF && i < len(frame) {
			out = append(out, 0)
		}
	}
	return out, nil
}

func nonzero(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i%254 + 1)
	}
	return b
}

func TestEncodeBoundaries(t *testing.T) {
	z254 := nonzero(254)
	z255 := nonzero(255)
	z508 := nonzero(508)
	cases := []struct {
		name string
		data []byte
		want []byte
		why  string
	}{
		{"empty", nil, []byte{0x01}, "empty data is one empty block: code 01"},
		{"single zero", []byte{0x00}, []byte{0x01, 0x01}, "empty run, then trailing zero forces a final empty block"},
		{"only zeros", []byte{0x00, 0x00, 0x00}, []byte{0x01, 0x01, 0x01, 0x01}, "each zero ends an empty run; last zero adds a final 01"},
		{"one byte", []byte{0x2A}, []byte{0x02, 0x2A}, "run of 1: code 02, data ends"},
		{"trailing zero", []byte{0x2A, 0x00}, []byte{0x02, 0x2A, 0x01}, "run of 1 consumes the zero; zero is last byte so a final 01 follows"},
		{"mid zero", []byte{0x2A, 0x00, 0x2B}, []byte{0x02, 0x2A, 0x02, 0x2B}, "zero consumed between two runs, no extra block"},
		{"254 nonzero", z254, append([]byte{0xFF}, z254...), "full 254 run: code FF, no implicit zero, no trailing 01"},
		{"254 nonzero + zero", append(append([]byte{}, z254...), 0x00), append(append([]byte{0xFF}, z254...), 0x01, 0x01), "FF block consumes no zero; the zero ends an empty run and is last, so 01 01"},
		{"255 nonzero", z255, append(append([]byte{0xFF}, z254...), 0x02, z255[254]), "254 full block, then a run of 1: code 02"},
		{"508 nonzero", z508, append(append([]byte{0xFF}, z254...), append([]byte{0xFF}, z254...)...), "two full FF blocks, no trailing 01"},
	}
	enc := &Encoder{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enc.Encode(tc.data)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			want := append(append([]byte{}, tc.want...), Delimiter)
			t.Logf("input=%x output=%x rationale=%s", tc.data, got, tc.why)
			if !bytes.Equal(got, want) {
				t.Fatalf("got %x, want %x", got, want)
			}
		})
	}
}

// feedAll feeds stream to a fresh decoder in chunks of size chunk and
// returns the delivered frames and final stats.
func feedAll(maxFrame, chunk int, stream []byte) ([][]byte, DecoderStats) {
	d := &Decoder{MaxFrame: maxFrame}
	var frames [][]byte
	for i := 0; i < len(stream); i += chunk {
		end := i + chunk
		if end > len(stream) {
			end = len(stream)
		}
		frames = append(frames, d.Write(stream[i:end])...)
	}
	return frames, d.Stats()
}

func TestDecodeValidAndEmpty(t *testing.T) {
	// Frames: "AB", empty (adjacent delimiters), empty data (01), "A\0B".
	stream := []byte{0x03, 'A', 'B', 0x00, 0x00, 0x01, 0x00, 0x02, 'A', 0x02, 'B', 0x00}
	frames, st := feedAll(0, 1, stream)
	want := [][]byte{[]byte("AB"), {}, []byte("A\x00B")}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames %q, want %d", len(frames), frames, len(want))
	}
	for i := range want {
		if !bytes.Equal(frames[i], want[i]) {
			t.Fatalf("frame %d = %q, want %q", i, frames[i], want[i])
		}
	}
	t.Logf("input=%x frames=%q stats=%+v rationale=adjacent delimiters count as Empties, 01 frame delivers empty data", stream, frames, st)
	if st.Frames != 3 || st.Empties != 1 || st.TooLong != 0 || st.Truncated != 0 || st.NonCanonical != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestDecodeRejects(t *testing.T) {
	z254 := nonzero(254)
	ffBlock := append([]byte{0xFF}, z254...)

	t.Run("truncated", func(t *testing.T) {
		// Code 03 promises 2 block bytes, frame ends after 1.
		stream := []byte{0x03, 'A', 0x00, 0x02, 'B', 0x00}
		frames, st := feedAll(0, 3, stream)
		t.Logf("input=%x frames=%q stats=%+v rationale=block cut by frame end is ErrTruncated, next frame still decodes", stream, frames, st)
		if len(frames) != 1 || !bytes.Equal(frames[0], []byte("B")) {
			t.Fatalf("frames = %q", frames)
		}
		if st.Truncated != 1 || st.Frames != 1 {
			t.Fatalf("stats = %+v", st)
		}
	})

	t.Run("non-canonical ff plus 01", func(t *testing.T) {
		// FF block followed by a redundant 01: decodes to 254 bytes
		// whose canonical form has no trailing 01.
		frame := append(append([]byte{}, ffBlock...), 0x01)
		stream := append(append([]byte{}, frame...), 0x00)
		frames, st := feedAll(0, 1, stream)
		t.Logf("input=%x frames=%q stats=%+v rationale=re-encode of 254 nonzero bytes drops the extra 01, so ErrNonCanonical", stream, frames, st)
		if len(frames) != 0 || st.NonCanonical != 1 {
			t.Fatalf("frames=%q stats=%+v", frames, st)
		}
	})

	t.Run("all-empty blocks are canonical", func(t *testing.T) {
		// 01 01 01 decodes to two zeros and IS the canonical form of
		// two zeros, so it must be delivered, not rejected.
		stream := []byte{0x01, 0x01, 0x01, 0x00}
		frames, st := feedAll(0, 2, stream)
		t.Logf("input=%x frames=%q stats=%+v rationale=01 01 01 re-encodes to itself, canonical", stream, frames, st)
		if len(frames) != 1 || !bytes.Equal(frames[0], []byte{0, 0}) {
			t.Fatalf("frames=%q stats=%+v", frames, st)
		}
		if st.Frames != 1 || st.NonCanonical != 0 {
			t.Fatalf("stats = %+v", st)
		}
	})

	t.Run("too long then resync", func(t *testing.T) {
		// MaxFrame 4: a 6-byte frame is dropped mid-stream; the
		// delimiter resynchronizes and the next frame decodes.
		stream := []byte{0x02, 'x', 0x02, 'y', 0x02, 'z', 0x00, 0x02, 'B', 0x00}
		frames, st := feedAll(4, 1, stream)
		t.Logf("input=%x frames=%q stats=%+v rationale=frame exceeds MaxFrame at 5th byte, dropped until next 00", stream, frames, st)
		if len(frames) != 1 || !bytes.Equal(frames[0], []byte("B")) {
			t.Fatalf("frames = %q", frames)
		}
		if st.TooLong != 1 || st.Frames != 1 {
			t.Fatalf("stats = %+v", st)
		}
	})

	t.Run("too long wins over other reasons", func(t *testing.T) {
		// This frame is both over MaxFrame and (its prefix)
		// non-canonical; only ErrFrameTooLong may be reported.
		stream := []byte{0x01, 0x01, 0x01, 0x01, 0x01, 0x00}
		_, st := feedAll(3, 6, stream)
		t.Logf("input=%x stats=%+v rationale=length check fires first, frame dropped before decode", stream, st)
		if st.TooLong != 1 || st.NonCanonical != 0 || st.Truncated != 0 {
			t.Fatalf("stats = %+v", st)
		}
	})

	t.Run("truncated wins over non-canonical", func(t *testing.T) {
		// 02 01 would decode oddly but the block is cut short first.
		stream := []byte{0x05, 'A', 0x00}
		_, st := feedAll(0, 5, stream)
		t.Logf("input=%x stats=%+v rationale=frame ends mid-block, reported as ErrTruncated only", stream, st)
		if st.Truncated != 1 || st.NonCanonical != 0 {
			t.Fatalf("stats = %+v", st)
		}
	})

	t.Run("error reasons are distinguishable", func(t *testing.T) {
		for _, err := range []error{ErrFrameTooLong, ErrTruncated, ErrNonCanonical, ErrDataTooLarge} {
			if !errors.Is(err, err) {
				t.Fatalf("errors.Is(%v, %v) = false", err, err)
			}
		}
		if errors.Is(ErrTruncated, ErrNonCanonical) || errors.Is(ErrFrameTooLong, ErrTruncated) {
			t.Fatal("rejection reasons must be distinct")
		}
	})
}

func TestChunkingIndependence(t *testing.T) {
	z254 := nonzero(254)
	enc := &Encoder{}
	var stream []byte
	for _, data := range [][]byte{[]byte("hello"), {}, []byte{0x00, 0x00}, z254, []byte("A\x00B")} {
		frame, err := enc.Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, frame...)
	}
	// Add an empty frame, a truncated frame, a non-canonical frame
	// (FF block with a redundant trailing 01) and an over-long frame
	// (MaxFrame will be 300) to exercise all counters.
	stream = append(stream, 0x00)
	stream = append(stream, 0x03, 'A', 0x00)
	stream = append(stream, 0xFF)
	stream = append(stream, z254...)
	stream = append(stream, 0x01, 0x00)
	stream = append(stream, bytes.Repeat([]byte{0x02, 'x'}, 200)...)
	stream = append(stream, 0x00)
	good, err := enc.Encode([]byte("tail"))
	if err != nil {
		t.Fatal(err)
	}
	stream = append(stream, good...)

	var refFrames [][]byte
	var refStats DecoderStats
	for _, chunk := range []int{1, 2, 3, 7, 64, len(stream)} {
		frames, st := feedAll(300, chunk, stream)
		t.Logf("chunk=%d frames=%d stats=%+v", chunk, len(frames), st)
		if refFrames == nil {
			refFrames, refStats = frames, st
			continue
		}
		if st != refStats {
			t.Fatalf("chunk=%d stats=%+v, want %+v", chunk, st, refStats)
		}
		if len(frames) != len(refFrames) {
			t.Fatalf("chunk=%d frames=%d, want %d", chunk, len(frames), len(refFrames))
		}
		for i := range frames {
			if !bytes.Equal(frames[i], refFrames[i]) {
				t.Fatalf("chunk=%d frame %d differs", chunk, i)
			}
		}
	}
	want := DecoderStats{Frames: 6, Empties: 1, TooLong: 1, Truncated: 1, NonCanonical: 1}
	if refStats != want {
		t.Fatalf("stats = %+v, want %+v", refStats, want)
	}
}

func TestRoundTripAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	enc := &Encoder{}
	for iter := 0; iter < 2000; iter++ {
		n := rng.Intn(1200)
		data := make([]byte, n)
		for i := range data {
			if rng.Intn(4) == 0 {
				data[i] = 0
			} else {
				data[i] = byte(1 + rng.Intn(255))
			}
		}
		frame, err := enc.Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := frame[:len(frame)-1]
		want := naiveEncode(data)
		if !bytes.Equal(got, want) {
			t.Fatalf("iter=%d input=%x\n got=%x\nwant=%x", iter, data, got, want)
		}
		dec, err := naiveDecode(want)
		if err != nil {
			t.Fatalf("iter=%d naiveDecode: %v", iter, err)
		}
		if !bytes.Equal(dec, data) {
			t.Fatalf("iter=%d round trip mismatch: input=%x decoded=%x", iter, data, dec)
		}
		d := &Decoder{}
		frames := d.Write(frame)
		if len(frames) != 1 || !bytes.Equal(frames[0], data) {
			t.Fatalf("iter=%d decoder frames=%q want %q", iter, frames, data)
		}
		if iter < 5 || iter%500 == 0 {
			t.Logf("iter=%d input=%x encoded=%x decoded-ok rationale=canonical form matches naive encoder and naive decoder round-trips", iter, data, got)
		}
	}
}

func TestEncoderMaxData(t *testing.T) {
	enc := &Encoder{MaxData: 3}
	before := enc.Stats()
	out, err := enc.Encode([]byte("toolong"))
	if !errors.Is(err, ErrDataTooLarge) {
		t.Fatalf("err = %v, want ErrDataTooLarge", err)
	}
	if out != nil {
		t.Fatalf("rejected encode produced output %x", out)
	}
	if got := enc.Stats(); got != before {
		t.Fatalf("stats changed on rejection: %+v -> %+v", before, got)
	}
	ok, err := enc.Encode([]byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input=%x output=%x rationale=len 3 <= MaxData 3 accepted, len 7 rejected with no output and no counter change", "abc", ok)
	if got := enc.Stats(); got.Frames != 1 || got.BytesIn != 3 || got.BytesOut != uint64(len(ok)) {
		t.Fatalf("stats = %+v", got)
	}
}

func TestConcurrency(t *testing.T) {
	enc := &Encoder{MaxData: 1024}
	dec := &Decoder{}
	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < perWorker; i++ {
				data := make([]byte, rng.Intn(300))
				for j := range data {
					data[j] = byte(rng.Intn(256))
				}
				frame, err := enc.Encode(data)
				if err != nil {
					t.Error(err)
					return
				}
				// One Write per frame keeps frames atomic under the
				// decoder mutex; goroutine interleaving then equals
				// some serial order of whole frames.
				dec.Write(frame)
			}
		}(w)
	}
	wg.Wait()
	es, ds := enc.Stats(), dec.Stats()
	t.Logf("encoder=%+v decoder=%+v rationale=concurrent calls equal some serial order, totals must match", es, ds)
	if es.Frames != workers*perWorker {
		t.Fatalf("encoder frames = %d", es.Frames)
	}
	if ds.Frames != workers*perWorker {
		t.Fatalf("decoder frames = %d, want %d", ds.Frames, workers*perWorker)
	}
	if ds.Truncated != 0 || ds.NonCanonical != 0 || ds.TooLong != 0 {
		t.Fatalf("unexpected rejections: %+v", ds)
	}
}
