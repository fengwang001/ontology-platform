package cobs

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
)

type stats struct {
	frames       uint64
	empties      uint64
	frameTooLong uint64
	truncated    uint64
	nonCanonical uint64
}

func snapshot(d *Decoder) stats {
	return stats{
		frames:       d.Frames(),
		empties:      d.Empties(),
		frameTooLong: d.FrameTooLongs(),
		truncated:    d.Truncated(),
		nonCanonical: d.NonCanonical(),
	}
}

func TestCanonicalGoldenCases(t *testing.T) {
	cases := []struct {
		name  string
		data  []byte
		frame []byte
	}{
		{"empty", []byte{}, []byte{0x01, 0x00}},
		{"single zero", []byte{0x00}, []byte{0x01, 0x01, 0x00}},
		{"one nonzero", []byte{0x42}, []byte{0x02, 0x42, 0x00}},
		{"one trailing zero", []byte{0x42, 0x00}, []byte{0x02, 0x42, 0x01, 0x00}},
		{"two zeros", []byte{0x00, 0x00}, []byte{0x01, 0x01, 0x01, 0x00}},
	}

	encoder := NewEncoder()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encoder.Encode(tc.data)
			if err != nil {
				t.Fatalf("Encode(% x): %v", tc.data, err)
			}
			t.Logf("input=% x output=% x reason=direct canonical block boundaries", tc.data, got)
			if !bytes.Equal(got, tc.frame) {
				t.Fatalf("Encode(% x) = % x, want % x", tc.data, got, tc.frame)
			}
		})
	}
}

func TestBoundaries(t *testing.T) {
	nonzero254 := repeated(1, 254)
	nonzero255 := repeated(1, 255)
	nonzero508 := repeated(1, 508)

	cases := [][]byte{
		nonzero254,
		append(append([]byte{}, nonzero254...), 0),
		nonzero255,
		nonzero508,
		append(append([]byte{}, nonzero508...), 0),
		append(append([]byte{}, nonzero508...), 1),
	}

	for _, data := range cases {
		frame, err := NewEncoder().Encode(data)
		if err != nil {
			t.Fatalf("len=%d: %v", len(data), err)
		}
		decodedFrames, result := decodeAll(frame)
		if len(decodedFrames) != 1 {
			t.Fatalf("len=%d frames=%d stats=%+v", len(data), len(decodedFrames), result.stats)
		}
		if !bytes.Equal(decodedFrames[0], data) {
			t.Fatalf("len=%d round trip mismatch", len(data))
		}
		t.Logf("input-len=%d output=% x output-len=%d reason=boundary block codes decode back", len(data), frame, len(frame))
	}
}

func TestFFBlockWithExtraCodeOneIsNonCanonical(t *testing.T) {
	frame := append([]byte{0xff}, repeated(1, 254)...)
	frame = append(frame, 0x01, 0x00)
	frames, result := decodeAll(frame)

	t.Logf("input=% x output-frames=% x reason=canonical 254-byte payload must omit trailing code 01", frame, frames)
	if len(frames) != 0 || result.stats.nonCanonical != 1 {
		t.Fatalf("frames=% x stats=%+v", frames, result.stats)
	}
}

func TestFrameErrorReasonsOrderingAndRecovery(t *testing.T) {
	truncated := []byte{0x02, 0x00}
	valid := []byte{0x02, 0x77, 0x00}

	frames, result := decodeAll(append(truncated, valid...))
	t.Logf("input=% x output=% x stats=%+v reason=code 02 needs one block byte before delimiter", append(truncated, valid...), frames, result.stats)
	if len(frames) != 1 || !bytes.Equal(frames[0], []byte{0x77}) || result.stats.truncated != 1 {
		t.Fatalf("frames=% x stats=%+v", frames, result.stats)
	}

	tooLong := append([]byte{0x01}, repeated(0x01, MaxFrame)...)
	tooLong = append(tooLong, 0x00)
	tooLong = append(tooLong, valid...)
	frames, result = decodeAll(tooLong)
	t.Logf("input-len=%d output=% x stats=%+v reason=frame exceeds MaxFrame before delimiter", len(tooLong), frames, result.stats)
	if len(frames) != 1 || result.stats.frameTooLong != 1 {
		t.Fatalf("frames=% x stats=%+v", frames, result.stats)
	}
}

func TestRandomRoundTripAgainstNaiveCodec(t *testing.T) {
	rng := rand.New(rand.NewPCG(1006, 2605))

	for iter := 0; iter < 400; iter++ {
		data := randomData(rng, rng.IntN(600))
		got, err := NewEncoder().Encode(data)
		if err != nil {
			t.Fatalf("iter=%d len=%d: %v", iter, len(data), err)
		}
		wantFrame := append(naiveEncode(data), 0)
		if !bytes.Equal(got, wantFrame) {
			t.Fatalf("iter=%d naive mismatch\ngot=% x\nwant=% x", iter, got, wantFrame)
		}

		frames, result := decodeAll(got)
		if len(frames) != 1 || !bytes.Equal(frames[0], data) || result.stats != (stats{frames: 1}) {
			t.Fatalf("iter=%d frames=% x stats=%+v", iter, frames, result.stats)
		}
		t.Logf("iter=%d input=% x output=% x reason=matches naive encoder and decodes identically", iter, data, got)
	}
}

func TestArbitraryWriteChunksHaveIdenticalResult(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 1001))
	stream := []byte{}
	for i := 0; i < 80; i++ {
		stream = append(stream, mustEncode(randomData(rng, rng.IntN(300)))...)
	}
	stream = append(stream, 0x00, 0x00)
	stream = append(stream, 0x02, 0x00)
	stream = append(stream, 0xff)
	stream = append(stream, repeated(1, 254)...)
	stream = append(stream, 0x01, 0x00)

	baselineFrames, baseline := decodeAll(stream)

	for chunkSize := 1; chunkSize <= 37; chunkSize++ {
		var got [][]byte
		var mu sync.Mutex
		d := NewDecoder(func(frame []byte) {
			mu.Lock()
			got = append(got, append([]byte{}, frame...))
			mu.Unlock()
		})
		for start := 0; start < len(stream); start += chunkSize {
			end := min(start+chunkSize, len(stream))
			if _, err := d.Write(stream[start:end]); err != nil {
				t.Fatalf("chunkSize=%d: %v", chunkSize, err)
			}
		}
		actual := stats{d.Frames(), d.Empties(), d.FrameTooLongs(), d.Truncated(), d.NonCanonical()}
		if !sameFrames(got, baselineFrames) || actual != baseline.stats {
			t.Fatalf("chunkSize=%d frames-equal=%v stats=%+v baseline=%+v", chunkSize, sameFrames(got, baselineFrames), actual, baseline)
		}
		t.Logf("chunk-size=%d frames=%d stats=%+v reason=delimiter-driven state is write-boundary independent", chunkSize, len(got), actual)
	}
}

func TestConcurrentUseAndReplay(t *testing.T) {
	encoder := NewEncoder()
	data := repeated(7, 300)
	const goroutines = 32
	var wg sync.WaitGroup
	frames := make([][]byte, goroutines)

	for i := range frames {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			frame, err := encoder.Encode(data)
			if err != nil {
				t.Errorf("Encode: %v", err)
				return
			}
			frames[i] = frame
		}(i)
	}
	wg.Wait()

	for _, frame := range frames {
		if !bytes.Equal(frame, frames[0]) {
			t.Fatal("concurrent encoder replay produced different frames")
		}
	}
	if encoder.Frames() != goroutines || encoder.Bytes() != uint64(goroutines*len(frames[0])) {
		t.Fatalf("frames=%d bytes=%d", encoder.Frames(), encoder.Bytes())
	}

	var decodedFrames [][]byte
	var mu sync.Mutex
	decoder := NewDecoder(func(frame []byte) {
		mu.Lock()
		decodedFrames = append(decodedFrames, append([]byte{}, frame...))
		mu.Unlock()
	})
	for range frames {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := decoder.Write(frames[0]); err != nil {
				t.Errorf("Write: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(decodedFrames) != goroutines || decoder.Frames() != goroutines {
		t.Fatalf("decoded=%d frames=%d", len(decodedFrames), decoder.Frames())
	}
	t.Logf("input=%d identical frames output=%d frames reason=mutex gives a serial equivalent order", len(data), len(decodedFrames))
}

func TestEncoderRejectsOversizeWithoutSideEffects(t *testing.T) {
	encoder := NewEncoder()
	beforeFrames := encoder.Frames()
	beforeBytes := encoder.Bytes()
	frame, err := encoder.Encode(make([]byte, MaxData+1))

	if frame != nil || err != ErrDataTooLong {
		t.Fatalf("frame=% x err=%v", frame, err)
	}
	if encoder.Frames() != beforeFrames || encoder.Bytes() != beforeBytes {
		t.Fatal("rejected encoding changed counters")
	}
}

func TestMaxDataAcceptedAndReasonsUseErrorsIs(t *testing.T) {
	data := make([]byte, MaxData)
	for i := range data {
		data[i] = byte(i%254 + 1)
	}
	frame, err := NewEncoder().Encode(data)
	if err != nil {
		t.Fatalf("MaxData encode: %v", err)
	}
	if len(frame) != MaxFrame+1 {
		t.Fatalf("frame len=%d, want %d", len(frame), MaxFrame+1)
	}
	frames, result := decodeAll(frame)
	if len(frames) != 1 || !bytes.Equal(frames[0], data) || result.stats != (stats{frames: 1}) {
		t.Fatalf("frames=%d stats=%+v", len(frames), result.stats)
	}
	t.Logf("input-len=%d output-len=%d reason=largest accepted payload uses exact MaxFrame encoding", len(data), len(frame))

	_, err = NewEncoder().Encode(append(append([]byte{}, data...), 0))
	if !errors.Is(err, ErrDataTooLong) {
		t.Fatalf("err=%v", err)
	}

	for _, tc := range []struct {
		name  string
		frame []byte
		want  error
	}{
		{"frame too long", repeated(1, MaxFrame+1), ErrFrameTooLong},
		{"truncated", []byte{0x02}, ErrTruncatedBlock},
		{"non canonical", append(append([]byte{0xff}, repeated(1, 254)...), 0x01), ErrNonCanonical},
	} {
		_, err := DecodeFrame(tc.frame)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
		if tc.want != ErrFrameTooLong {
			_, result = decodeAll(append(append([]byte{}, tc.frame...), 0))
		}
		t.Logf("case=%s input=% x reason=frame decoder returns %v", tc.name, tc.frame, tc.want)
	}
}

func decodeAll(p []byte) ([][]byte, decodedResult) {
	var frames [][]byte
	d := NewDecoder(func(frame []byte) {
		frames = append(frames, append([]byte{}, frame...))
	})
	_, _ = d.Write(p)
	return frames, decodedResult{frames: frames, stats: snapshot(d)}
}

type decodedResult struct {
	frames [][]byte
	stats  stats
}

func mustEncode(data []byte) []byte {
	frame, err := NewEncoder().Encode(data)
	if err != nil {
		panic(err)
	}
	return frame
}

func naiveEncode(data []byte) []byte {
	out := []byte{}
	for pos := 0; pos <= len(data); {
		run := 0
		for pos+run < len(data) && data[pos+run] != 0 && run < 254 {
			run++
		}
		out = append(out, byte(run+1))
		out = append(out, data[pos:pos+run]...)
		pos += run

		if run == 254 {
			continue
		}
		if pos == len(data) {
			break
		}
		pos++
		if pos == len(data) {
			out = append(out, 1)
			break
		}
	}
	if len(data) == 0 {
		out = []byte{1}
	}
	return out
}

func randomData(rng *rand.Rand, n int) []byte {
	data := make([]byte, n)
	for i := range data {
		switch rng.IntN(8) {
		case 0:
			data[i] = 0
		case 1:
			data[i] = byte(1 + rng.IntN(254))
		default:
			data[i] = byte(rng.IntN(256))
		}
	}
	return data
}

func repeated(value byte, n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = value
	}
	return data
}

func sameFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
