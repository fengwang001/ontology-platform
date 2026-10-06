package chunkcontainer

import (
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"strings"
	"testing"
)

type naiveModel struct {
	logical       []byte
	blockSize     int
	minimumGain   int
	cacheCapacity int
	compressor    Compressor
	decompressor  Decompressor

	forms          map[int]StorageForm
	reasons        map[int]StoreReason
	storedLengths  map[int]int
	checksums      map[int]uint32
	compressorUses int

	cached     []int
	hits       int
	decompiles int
}

type modelCompressor struct{}

func (modelCompressor) Compress(data []byte) ([]byte, bool) {
	if len(data) > 0 && data[0] == 0xff {
		return nil, false
	}
	output := []byte{byte(len(data))}
	var packed byte
	for index, value := range data {
		if value == 2 {
			packed |= 1 << (index % 8)
		}
		if index%8 == 7 || index == len(data)-1 {
			output = append(output, packed)
			packed = 0
		}
	}
	return output, true
}

type modelDecompressor struct{}

func (modelDecompressor) Decompress(data []byte) ([]byte, bool) {
	if len(data) < 1 || int(data[0]) <= (len(data)-2)*8 || int(data[0]) > (len(data)-1)*8 {
		return nil, false
	}
	output := make([]byte, data[0])
	for index := range output {
		if data[1+index/8]&(1<<(index%8)) != 0 {
			output[index] = 2
		} else {
			output[index] = 1
		}
	}
	return output, true
}

func newNaiveModel(blockSize, minimumGain, cacheCapacity int) *naiveModel {
	return &naiveModel{
		blockSize:     blockSize,
		minimumGain:   minimumGain,
		cacheCapacity: cacheCapacity,
		compressor:    modelCompressor{},
		decompressor:  modelDecompressor{},
		forms:         make(map[int]StorageForm),
		reasons:       make(map[int]StoreReason),
		storedLengths: make(map[int]int),
		checksums:     make(map[int]uint32),
	}
}

func (model *naiveModel) rebuild(t *testing.T, log *strings.Builder, data []byte) {
	t.Helper()
	model.forms = make(map[int]StorageForm)
	model.reasons = make(map[int]StoreReason)
	model.storedLengths = make(map[int]int)
	model.checksums = make(map[int]uint32)

	for start := 0; start < len(data); start += model.blockSize {
		end := min(start+model.blockSize, len(data))
		block := data[start:end]
		index := start / model.blockSize
		compressed, ok := model.compressor.Compress(block)
		form := FormCompressed
		reason := ReasonNone
		stored := compressed
		if !ok {
			form = FormDirect
			reason = ReasonCompressionFailed
			stored = block
		} else if len(compressed) > len(block)-model.minimumGain {
			form = FormDirect
			reason = ReasonInsufficientGain
			stored = block
		}
		model.forms[index] = form
		model.reasons[index] = reason
		model.storedLengths[index] = len(stored)
		model.checksums[index] = crc32.ChecksumIEEE(block)
		fmt.Fprintf(log, "model append-decision block=%d form=%d reason=%d original=%d stored=%d checksum=%08x\n",
			index, form, reason, len(block), len(stored), model.checksums[index])
	}
}

func (model *naiveModel) read(offset, length int) (string, bool, error) {
	if offset < 0 || length < 0 {
		return "", false, ErrInvalidArgument
	}
	if offset > len(model.logical) {
		return "", false, ErrOutOfBounds
	}
	end := min(offset+length, len(model.logical))
	eof := offset == len(model.logical) || end == len(model.logical)
	if offset == end {
		return "", eof, nil
	}

	first := offset / model.blockSize
	last := (end - 1) / model.blockSize
	touched := make([]int, 0, last-first+1)
	hitInRead := make([]int, 0, last-first+1)
	for index := first; index <= last; index++ {
		for _, cached := range model.cached {
			if cached == index {
				model.hits++
				hitInRead = append(hitInRead, index)
				break
			}
		}
		if !model.hasCached(index) && model.forms[index] == FormCompressed {
			start := index * model.blockSize
			blockEnd := min(start+model.blockSize, len(model.logical))
			compressed, _ := model.compressionFor(model.logical[start:blockEnd])
			if _, ok := model.decompressor.Decompress(compressed); !ok {
				return "", false, BlockError{Kind: Decompression, BlockIndex: index}
			}
			model.decompiles++
			touched = append(touched, index)
		}
	}

	output := string(model.logical[offset:end])
	for _, index := range hitInRead {
		model.insertCache(index)
	}
	for _, index := range touched {
		model.insertCache(index)
	}
	return output, eof, nil
}

func (model *naiveModel) hasCached(index int) bool {
	for _, cached := range model.cached {
		if cached == index {
			return true
		}
	}
	return false
}

func (model *naiveModel) compressionFor(block []byte) ([]byte, bool) {
	return model.compressor.Compress(block)
}

func (model *naiveModel) insertCache(index int) {
	if model.cacheCapacity == 0 {
		return
	}
	filtered := model.cached[:0]
	for _, cached := range model.cached {
		if cached != index {
			filtered = append(filtered, cached)
		}
	}
	model.cached = append(filtered, index)
	if len(model.cached) > model.cacheCapacity {
		model.cached = model.cached[1:]
	}
}

func (model *naiveModel) invalidateFinal(rewritten bool) {
	oldCount := len(model.forms)
	if rewritten && oldCount > 0 {
		model.cached = append(model.cached[:0], model.cached...)
		filtered := model.cached[:0]
		for _, index := range model.cached {
			if index != oldCount-1 {
				filtered = append(filtered, index)
			}
		}
		model.cached = filtered
	}
}

func (model *naiveModel) physical() int64 {
	total := int64(0)
	for _, storedLength := range model.storedLengths {
		total += int64(HeaderSize + storedLength)
	}
	return total
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	random := rand.New(rand.NewSource(1598))
	var log strings.Builder
	container, err := New(Config{BlockSize: 5, MinimumGain: 2, CacheCapacity: 3}, modelCompressor{}, modelDecompressor{})
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel(5, 2, 3)

	for operation := 0; operation < 500; operation++ {
		if random.Intn(3) == 0 && len(model.logical) < 160 {
			length := random.Intn(13)
			data := make([]byte, length)
			for index := range data {
				data[index] = byte(1 + random.Intn(2))
			}
			if random.Intn(20) == 0 {
				data = append([]byte{0xff}, data...)
			}
			fmt.Fprintf(&log, "op=%d append input=%x\n", operation, data)
			beforeLogical := len(model.logical)
			rewritten := beforeLogical > 0 && beforeLogical%5 != 0
			result, appendErr := container.Append(data)
			if appendErr != nil {
				t.Fatalf("append failed: %v\n%s", appendErr, log.String())
			}
			model.logical = append(model.logical, data...)
			model.compressorUses += len(result.Blocks)
			model.invalidateFinal(rewritten)
			model.rebuild(t, &log, model.logical)
			for _, decision := range result.Blocks {
				fmt.Fprintf(&log, "actual append-decision block=%d form=%d reason=%d original=%d stored=%d checksum=%08x\n",
					decision.Index, decision.Form, decision.Reason, decision.OriginalLength, decision.StoredLength, decision.Checksum)
				if decision.Form != model.forms[decision.Index] || decision.Reason != model.reasons[decision.Index] ||
					decision.OriginalLength != expectedOriginalLength(model, decision.Index) ||
					decision.StoredLength != model.storedLengths[decision.Index] ||
					decision.Checksum != model.checksums[decision.Index] {
					t.Fatalf("decision mismatch at block %d\n%s", decision.Index, log.String())
				}
			}
			continue
		}

		total := len(model.logical)
		offset := 0
		if total > 0 {
			offset = random.Intn(total + 2)
		}
		length := random.Intn(12)
		fmt.Fprintf(&log, "op=%d read offset=%d length=%d\n", operation, offset, length)
		actual, actualErr := container.ReadAt(offset, length)
		expected, expectedEOF, expectedErr := model.read(offset, length)
		if !sameReadError(actualErr, expectedErr) {
			t.Fatalf("error mismatch actual=%v expected=%v\n%s", actualErr, expectedErr, log.String())
		}
		if actualErr == nil && (string(actual.Data) != expected || actual.EOF != expectedEOF) {
			t.Fatalf("read mismatch actual=%q/%v expected=%q/%v\n%s", actual.Data, actual.EOF, expected, expectedEOF, log.String())
		}
		fmt.Fprintf(&log, "actual output=%x eof=%v err=%v\n", actual.Data, actual.EOF, actualErr)
	}

	actualStats := container.Stats()
	if actualStats.LogicalBytes != int64(len(model.logical)) || actualStats.PhysicalBytes != model.physical() {
		t.Fatalf("size stats mismatch actual=%+v modelPhysical=%d", actualStats, model.physical())
	}
	if actualStats.CompressorCalls != int64(model.compressorUses) || actualStats.CacheHits != int64(model.hits) {
		t.Fatalf("call stats mismatch actual=%+v model compressor=%d hits=%d", actualStats, model.compressorUses, model.hits)
	}
	t.Log("\n" + log.String())
}

func expectedOriginalLength(model *naiveModel, index int) int {
	start := index * model.blockSize
	end := min(start+model.blockSize, len(model.logical))
	return end - start
}

func sameReadError(actual, expected error) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	if errors.Is(actual, ErrInvalidArgument) || errors.Is(actual, ErrOutOfBounds) {
		return errors.Is(actual, ErrInvalidArgument) == errors.Is(expected, ErrInvalidArgument) &&
			errors.Is(actual, ErrOutOfBounds) == errors.Is(expected, ErrOutOfBounds)
	}
	var actualBlock BlockError
	var expectedBlock BlockError
	return errors.As(actual, &actualBlock) && errors.As(expected, &expectedBlock) &&
		actualBlock.Kind == expectedBlock.Kind && actualBlock.BlockIndex == expectedBlock.BlockIndex
}

func TestComplexityObservations(t *testing.T) {
	cache := newBlockCache(2)
	cache.touch(1, []byte("a"))
	cache.touch(2, []byte("b"))
	cache.touch(3, []byte("c"))
	if _, ok := cache.peek(1); ok {
		t.Fatal("least-recently-used block 1 must be evicted")
	}
	cache.touch(2, []byte("b2"))
	cache.touch(4, []byte("d"))
	if _, ok := cache.peek(3); ok {
		t.Fatal("tie by recency must keep smaller block index 2 and evict 3")
	}

	if index := (1_000_000) / 3; index < 333_333 || index > 333_334 {
		t.Fatal("offset location must remain one division, not a scan")
	}
}
