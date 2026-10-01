package hdlc

import (
	"math/rand"
	"testing"
)

type naiveResult struct {
	frames [][]byte
	stats  Stats
}

func TestRandomAgainstBitwiseNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iteration := 0; iteration < 300; iteration++ {
		runRandomNaiveCase(t, rng, iteration, false)
	}
}

func TestRandomAgainstBitwiseNaiveLogged(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iteration := 0; iteration < 3; iteration++ {
		runRandomNaiveCase(t, rng, iteration+1000, true)
	}
}

func runRandomNaiveCase(t *testing.T, rng *rand.Rand, iteration int, logBits bool) {
	t.Helper()
	bits, want := buildRandomNaiveCase(t, rng, iteration)
	data := bitsToTestBytes(padBits(bits))

	t.Run("whole", func(t *testing.T) {
		decoder := NewDecoder()
		frames, err := decoder.Write(data)
		if err != nil {
			t.Fatal(err)
		}
		if logBits {
			t.Logf("iteration=%d input bits=%s bytes=% x; output frames=%x stats=%+v; naive frames=%x stats=%+v; decision=compare complete flags, seven-one aborts, destuffed length and CRC in required order", iteration, formatBits(bits), data, frames, decoder.Stats(), want.frames, want.stats)
		} else {
			t.Logf("iteration=%d input bytes=% x; output frames=%d stats=%+v; decision=bitwise flags, aborts, alignment and FCS match", iteration, data, len(frames), decoder.Stats())
		}
		assertNaiveResult(t, frames, decoder.Stats(), want)
	})

	for chunkSize := 1; chunkSize <= len(data); chunkSize++ {
		decoder := NewDecoder()
		frames := decodeInChunks(t, decoder, data, chunkSize)
		if got := (naiveResult{frames: frames, stats: decoder.Stats()}); !naiveResultEqual(got, want) {
			t.Fatalf("iteration=%d chunk=%d frames=%x stats=%+v, want frames=%x stats=%+v; input bits=%s", iteration, chunkSize, frames, decoder.Stats(), want.frames, want.stats, formatBits(bits))
		}
	}
}

func buildRandomNaiveCase(t *testing.T, rng *rand.Rand, iteration int) ([]byte, naiveResult) {
	t.Helper()

	var bits []byte
	started := false

	emitFlag := func() {
		bits = append(bits, flagBits()...)
		started = true
	}
	emitFrame := func(payload []byte) {
		if !started {
			emitFlag()
		}
		frameBits := completeFrameBits(t, payload)
		bits = append(bits, trimThroughLastFlag(frameBits)[8:]...)
	}

	events := rng.Intn(8) + 2
	for event := 0; event < events; event++ {
		if rng.Intn(7) == 0 {
			if started {
				bits = append(bits, bytesToBits([]byte{0xFF})...)
				started = false
			} else {
				bits = append(bits, randomNoiseBits(rng, rng.Intn(9)+1)...)
			}
			continue
		}

		payload := make([]byte, rng.Intn(12)+1)
		for i := range payload {
			payload[i] = byte(rng.Intn(256))
		}
		emitFrame(payload)

		switch rng.Intn(8) {
		case 0:
			bits = append(bits, flagBits()[1:]...)
		case 1:
			bits = append(bits, bytesToBits([]byte{0xFF})...)
			started = false
		case 2:
			bits = append(bits, randomDamageBits(rng, 1+rng.Intn(3))...)
		}
	}

	if len(bits)%8 != 0 {
		bits = padBits(bits)
	}
	return bits, naiveDecode(bits)
}

func naiveDecode(bits []byte) naiveResult {
	var result naiveResult
	var window []byte
	inFrame := false
	var raw []byte
	ones := 0
	rawCut := 0

	for _, bit := range bits {
		window = append(window, bit)
		if len(window) > 8 {
			window = window[1:]
		}

		if !inFrame {
			if len(window) == 8 && bitsEqual(window, flagBits()) {
				inFrame = true
				window = nil
				raw = nil
				rawCut = 0
				ones = 0
			}
			continue
		}

		if len(window) == 8 && bitsEqual(window, flagBits()) {
			frameRaw := raw
			if len(frameRaw) >= 8 && rawCut > 0 {
				frameRaw = frameRaw[rawCut : len(frameRaw)-7]
			} else if len(frameRaw) >= 7 {
				frameRaw = frameRaw[:len(frameRaw)-7]
			} else {
				frameRaw = nil
			}
			content := naiveDestuff(frameRaw)
			if len(content) > 0 {
				if len(content)%8 != 0 {
					result.stats.AlignmentErrors++
				} else if len(content) < 24 {
					result.stats.ShortFrames++
				} else {
					data := bitsToBytes(content)
					payload := data[:len(data)-2]
					fcs := uint16(data[len(data)-2]) | uint16(data[len(data)-1])<<8
					if CRC16X25(payload) != fcs {
						result.stats.FCSErrors++
					} else {
						result.frames = append(result.frames, append([]byte(nil), payload...))
						result.stats.Frames++
					}
				}
			}
			inFrame = true
			window = []byte{0}
			raw = []byte{0}
			rawCut = 1
			ones = 0
			continue
		}

		if bit == 1 {
			ones++
		} else {
			ones = 0
		}
		if ones >= 7 {
			result.stats.Aborts++
			inFrame = false
			window = nil
			raw = nil
			ones = 0
			continue
		}
		raw = append(raw, bit)
	}
	return result
}

func naiveDestuff(raw []byte) []byte {
	content := make([]byte, 0, len(raw))
	ones := 0
	for _, bit := range raw {
		if bit == 0 {
			if ones == 5 {
				ones = 0
				continue
			}
			ones = 0
			content = append(content, 0)
			continue
		}
		ones++
		content = append(content, 1)
	}
	return content
}

func randomNoiseBits(rng *rand.Rand, count int) []byte {
	bits := make([]byte, count)
	for i := range bits {
		bits[i] = byte(rng.Intn(2))
	}
	return bits
}

func randomDamageBits(rng *rand.Rand, count int) []byte {
	bits := make([]byte, count)
	for i := range bits {
		bits[i] = byte(rng.Intn(2))
	}
	return bits
}

func bitsEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func naiveResultEqual(left, right naiveResult) bool {
	return frameSlicesEqual(left.frames, right.frames) && left.stats == right.stats
}

func assertNaiveResult(t *testing.T, frames [][]byte, stats Stats, want naiveResult) {
	t.Helper()
	assertFrames(t, frames, want.frames)
	assertStats(t, stats, want.stats)
}
