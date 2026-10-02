package ontology

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"strings"
	"testing"
)

type naiveModel struct {
	bits           strings.Builder
	start          int64
	lastTime       int64
	lastDelta      int64
	lastValue      uint64
	hasWindow      bool
	windowLZ       int
	windowTZ       int
	count          int
	maxBits        int
	rejections     int
	acceptedTimes  []int64
	acceptedValues []uint64
}

func TestRandomSequencesMatchNaiveBitModel(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x51e1_7a11_2345_6789, 0x9abc_def0_1234_5678))
	const iterations = 2000
	totalRejections := 0

	for iteration := 0; iteration < iterations; iteration++ {
		start := int64(rng.Int64())
		maxBytes := 64 + rng.IntN(1024)
		if iteration%3 == 0 {
			maxBytes = 18 + rng.IntN(16)
		}
		block, err := New(start, maxBytes)
		if err != nil {
			t.Fatalf("iteration %d New: %v", iteration, err)
		}
		model := newNaiveModel(start, maxBytes)

		sampleCount := 1 + rng.IntN(18)
		input := make([]Sample, 0, sampleCount)
		var currentDelta int64
		currentTime := start
		for index := 0; index < sampleCount; index++ {
			if index == 0 {
				currentTime = start + int64(rng.Int64N(15360))
				currentDelta = currentTime - start
			} else {
				currentDelta += int64(rng.IntN(81)) - 40
				if currentDelta < 0 {
					currentDelta = 0
				}
				currentTime += currentDelta
			}
			value := randomFloatBits(rng)
			input = append(input, Sample{Timestamp: currentTime, Value: math.Float64frombits(value)})

			blockErr := block.Append(currentTime, math.Float64frombits(value))
			modelErr := model.append(currentTime, value)
			if (blockErr == ErrFull) != (modelErr == errNaiveFull) {
				t.Fatalf("iteration %d append %d capacity mismatch: block=%v model=%v; input=%v",
					iteration, index, blockErr, modelErr, input)
			}
			if blockErr == nil && modelErr != nil {
				t.Fatalf("iteration %d model rejected but block accepted: %v; input=%v", iteration, modelErr, input)
			}
			if blockErr == ErrFull {
				model.rejections++
			}
		}
		totalRejections += model.rejections

		if err := block.Seal(); err != nil {
			t.Fatalf("iteration %d Seal: %v", iteration, err)
		}
		model.seal()

		gotBits := bitString(block.Bytes(), block.Bits())
		wantBits := model.bits.String()
		if gotBits != wantBits {
			t.Fatalf("iteration %d bitstream mismatch\ninput=%v\n got=%s\nwant=%s\n判定依据：朴素位串逐字段拼接",
				iteration, input, gotBits, wantBits)
		}
		if block.Len() != model.count {
			t.Fatalf("iteration %d Len=%d model=%d", iteration, block.Len(), model.count)
		}

		decodedStart, decoded, err := Decode(block.Bytes())
		if err != nil {
			t.Fatalf("iteration %d Decode: %v; input=%v bits=%s", iteration, err, input, gotBits)
		}
		if decodedStart != start || len(decoded) != model.count {
			t.Fatalf("iteration %d decoded start=%d count=%d, want %d and %d",
				iteration, decodedStart, len(decoded), start, model.count)
		}
		for index, sample := range decoded {
			acceptedTime := model.acceptedTimes[index]
			acceptedValue := model.acceptedValues[index]
			if sample.Timestamp != acceptedTime || math.Float64bits(sample.Value) != acceptedValue {
				t.Fatalf("iteration %d decoded[%d]=%+v bits=%016x, want time=%d value=%016x",
					iteration, index, sample, math.Float64bits(sample.Value), acceptedTime, acceptedValue)
			}
		}

		if iteration < 5 {
			t.Logf("iteration=%d input=%v output bits=%s samples=%d rejections=%d; 判定依据：块位流与朴素位串完全相等且往返",
				iteration, input, gotBits, len(decoded), model.rejections)
		}
	}
	if totalRejections == 0 {
		t.Fatalf("random test produced no ErrFull cases; 判定依据：小容量批次应覆盖原子容量拒绝")
	}
	t.Logf("random model total rejections=%d over %d iterations; 判定依据：容量拒绝与朴素模型容量判定一致", totalRejections, iterations)
}

var errNaiveFull = naiveFullError{}

type naiveFullError struct{}

func (naiveFullError) Error() string { return "naive full" }

func newNaiveModel(start int64, maxBytes int) *naiveModel {
	model := &naiveModel{start: start, maxBits: maxBytes * 8}
	model.writeUint(uint64(start), 64)
	return model
}

func (m *naiveModel) append(timestamp int64, value uint64) error {
	var candidate strings.Builder
	candidate.WriteString(m.bits.String())
	var delta int64
	var dod int64

	if m.count == 0 {
		delta = timestamp - m.start
		if delta < 0 || delta > 15359 {
			return ErrDelta
		}
		writeStringUint(&candidate, uint64(delta), 14)
		writeStringUint(&candidate, value, 64)
	} else {
		delta = timestamp - m.lastTime
		dod = delta - m.lastDelta
		if timestamp < m.lastTime || dod < -1<<31 || dod > 1<<31-1 {
			if timestamp < m.lastTime {
				return ErrOrder
			}
			return ErrDelta
		}
		writeTimestampDODString(&candidate, dod)
		writeValueXORString(&candidate, value, m.lastValue, m.hasWindow, m.windowLZ, m.windowTZ)
	}

	if candidate.Len()+36 > m.maxBits {
		return errNaiveFull
	}

	m.bits.Reset()
	m.bits.WriteString(candidate.String())
	if m.count > 0 {
		xor := value ^ m.lastValue
		if xor != 0 {
			lz := min(bits.LeadingZeros64(xor), 31)
			tz := bits.TrailingZeros64(xor)
			reuse := m.hasWindow && lz >= m.windowLZ && tz >= m.windowTZ
			if reuse {
				reuseCost := 2 + 64 - m.windowLZ - m.windowTZ
				openCost := 13 + 64 - lz - tz
				if openCost < reuseCost {
					reuse = false
				}
			}
			if !reuse {
				m.hasWindow = true
				m.windowLZ = lz
				m.windowTZ = tz
			}
		}
	}
	m.lastTime = timestamp
	m.lastDelta = delta
	m.lastValue = value
	m.count++
	m.acceptedTimes = append(m.acceptedTimes, timestamp)
	m.acceptedValues = append(m.acceptedValues, value)
	return nil
}

func (m *naiveModel) seal() {
	m.bits.WriteString("1111")
	writeStringUint(&m.bits, 0, 32)
}

func writeTimestampDODString(builder *strings.Builder, dod int64) {
	switch {
	case dod == 0:
		builder.WriteByte('0')
	case dod >= -63 && dod <= 64:
		builder.WriteString("10")
		writeStringUint(builder, uint64(dod), 7)
	case dod >= -255 && dod <= 256:
		builder.WriteString("110")
		writeStringUint(builder, uint64(dod), 9)
	case dod >= -2047 && dod <= 2048:
		builder.WriteString("1110")
		writeStringUint(builder, uint64(dod), 12)
	default:
		builder.WriteString("1111")
		writeStringUint(builder, uint64(dod), 32)
	}
}

func writeValueXORString(builder *strings.Builder, value, previous uint64, hasWindow bool, windowLZ, windowTZ int) {
	xor := value ^ previous
	if xor == 0 {
		builder.WriteByte('0')
		return
	}
	lz := min(bits.LeadingZeros64(xor), 31)
	tz := bits.TrailingZeros64(xor)
	reuse := hasWindow && lz >= windowLZ && tz >= windowTZ
	if reuse {
		reuseCost := 2 + 64 - windowLZ - windowTZ
		openCost := 13 + 64 - lz - tz
		if openCost < reuseCost {
			reuse = false
		}
	}
	if reuse {
		builder.WriteString("10")
		writeStringUint(builder, xor>>uint(windowTZ), 64-windowLZ-windowTZ)
		return
	}
	builder.WriteString("11")
	writeStringUint(builder, uint64(lz), 5)
	length := 64 - lz - tz
	if length == 64 {
		writeStringUint(builder, 0, 6)
	} else {
		writeStringUint(builder, uint64(length), 6)
	}
	writeStringUint(builder, xor>>uint(tz), length)
}

func writeStringUint(builder *strings.Builder, value uint64, width int) {
	for index := width - 1; index >= 0; index-- {
		if value&(1<<uint(index)) != 0 {
			builder.WriteByte('1')
		} else {
			builder.WriteByte('0')
		}
	}
}

func (m *naiveModel) writeUint(value uint64, width int) {
	writeStringUint(&m.bits, value, width)
}

func bitString(data []byte, validBits int) string {
	var builder strings.Builder
	for index := 0; index < validBits; index++ {
		if data[index/8]&(1<<(7-uint(index%8))) != 0 {
			builder.WriteByte('1')
		} else {
			builder.WriteByte('0')
		}
	}
	return builder.String()
}

func randomFloatBits(rng *rand.Rand) uint64 {
	switch rng.IntN(8) {
	case 0:
		return 0
	case 1:
		return 1 << 63
	case 2:
		return rng.Uint64() | (0x7ff << 52)
	case 3:
		return 1 << uint(rng.IntN(64))
	default:
		return rng.Uint64()
	}
}
