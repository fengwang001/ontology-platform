package chunkcontainer

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"sync"
	"testing"
)

type scriptCompressor struct {
	mu       sync.Mutex
	calls    int
	failNext bool
}

func (compressor *scriptCompressor) Compress(data []byte) ([]byte, bool) {
	compressor.mu.Lock()
	defer compressor.mu.Unlock()
	compressor.calls++
	if compressor.failNext || len(data) > 0 && data[0] == 0xff {
		compressor.failNext = false
		return nil, false
	}
	result := make([]byte, len(data))
	copy(result, data)
	return result, true
}

type identityDecompressor struct{}

func (identityDecompressor) Decompress(data []byte) ([]byte, bool) {
	result := make([]byte, len(data))
	copy(result, data)
	return result, true
}

type failDecompressor struct{}

func (failDecompressor) Decompress(data []byte) ([]byte, bool) { return nil, false }

func TestThresholdAndFailureDecisions(t *testing.T) {
	t.Run("stored length equal threshold is compressed", func(t *testing.T) {
		container, err := New(Config{BlockSize: 10, MinimumGain: 3, CacheCapacity: 1}, fixedSizeCompressor{4}, identityDecompressor{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := container.Append([]byte("0123456"))
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Blocks) != 1 || result.Blocks[0].Form != FormCompressed || result.Blocks[0].StoredLength != 4 {
			t.Fatalf("unexpected decision: %+v", result.Blocks)
		}
	})

	t.Run("one byte longer than threshold is direct", func(t *testing.T) {
		container := newTestContainer(t, 10, 3, 1)
		record := decideBlock(append([]byte{}, "01234567"...), 3, fixedSizeCompressor{8})
		if record.form != FormDirect || record.reason != ReasonInsufficientGain {
			t.Fatalf("got form=%v reason=%v", record.form, record.reason)
		}
		if container.Stats().CompressorCalls != 0 {
			t.Fatal("decision must not mutate container stats")
		}
	})

	t.Run("compression failure is distinct", func(t *testing.T) {
		compressor := &scriptCompressor{failNext: true}
		container, err := New(Config{BlockSize: 10, MinimumGain: 3}, compressor, identityDecompressor{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := container.Append([]byte("01234"))
		if err != nil {
			t.Fatal(err)
		}
		if result.Blocks[0].Form != FormDirect || result.Blocks[0].Reason != ReasonCompressionFailed {
			t.Fatalf("unexpected decision: %+v", result.Blocks[0])
		}
		stats := container.Stats()
		if stats.CompressionFailureBlocks != 1 || stats.InsufficientGainBlocks != 0 || stats.CompressorCalls != 1 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
	})

	t.Run("original length not greater than minimum gain always uses same rule", func(t *testing.T) {
		record := decideBlock([]byte{1, 2}, 5, fixedSizeCompressor{0})
		if record.form != FormDirect || record.reason != ReasonInsufficientGain {
			t.Fatalf("got form=%v reason=%v", record.form, record.reason)
		}
		record = decideBlock([]byte{1, 2}, 5, fixedSizeCompressor{1})
		if record.form != FormDirect || record.reason != ReasonInsufficientGain {
			t.Fatalf("got form=%v reason=%v", record.form, record.reason)
		}
	})
}

type fixedSizeCompressor struct{ outputLength int }

func (compressor fixedSizeCompressor) Compress(data []byte) ([]byte, bool) {
	if compressor.outputLength == -1 {
		return nil, false
	}
	return make([]byte, compressor.outputLength), true
}

func newTestContainer(t *testing.T, blockSize, minimumGain, cacheCapacity int) *Container {
	t.Helper()
	container, err := New(Config{BlockSize: blockSize, MinimumGain: minimumGain, CacheCapacity: cacheCapacity}, &scriptCompressor{}, identityDecompressor{})
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func TestAppendBoundariesAndReads(t *testing.T) {
	container := newTestContainer(t, 4, 1, 2)
	appendAndCheck(t, container, "abc", 1)
	appendAndCheck(t, container, "d", 1)
	readAndCheck(t, container, 0, 4, "abcd", true)
	appendAndCheck(t, container, "efg", 1)
	readAndCheck(t, container, 4, 4, "efg", true)
	readAndCheck(t, container, 7, 10, "", true)

	result, err := container.Append(nil)
	if err != nil || len(result.Blocks) != 0 {
		t.Fatalf("zero append must be no-op, result=%+v err=%v", result, err)
	}

	if _, err := container.ReadAt(8, 1); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("expected out of bounds, got %v", err)
	}
	if _, err := container.ReadAt(-1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid argument, got %v", err)
	}
	stats := container.Stats()
	if stats.LogicalBytes != 7 || stats.PhysicalBytes != int64(2*HeaderSize+4+3) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func appendAndCheck(t *testing.T, container *Container, data string, expectedBlocks int) {
	t.Helper()
	result, err := container.Append([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != expectedBlocks {
		t.Fatalf("append %q expected %d decisions, got %+v", data, expectedBlocks, result.Blocks)
	}
}

func readAndCheck(t *testing.T, container *Container, offset, length int, expected string, eof bool) {
	t.Helper()
	result, err := container.ReadAt(offset, length)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != expected || result.EOF != eof {
		t.Fatalf("read(%d,%d) got %q eof=%v, want %q eof=%v", offset, length, result.Data, result.EOF, expected, eof)
	}
}

func TestCacheBoundariesAndRewriteInvalidation(t *testing.T) {
	t.Run("capacity zero never caches", func(t *testing.T) {
		container := newTestContainer(t, 4, 0, 0)
		appendData(t, container, "abcdef")
		readAndCheck(t, container, 0, 6, "abcdef", true)
		readAndCheck(t, container, 0, 6, "abcdef", true)
		stats := container.Stats()
		if stats.DecompressorCalls != 4 || stats.CacheHits != 0 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
	})

	t.Run("capacity one evicts LRU and rewrite invalidates", func(t *testing.T) {
		container := newTestContainer(t, 4, 0, 1)
		appendData(t, container, "abcdef")
		readAndCheck(t, container, 0, 5, "abcde", false)
		stats := container.Stats()
		if stats.DecompressorCalls != 2 || stats.CacheHits != 0 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
		readAndCheck(t, container, 0, 5, "abcde", false)
		if stats := container.Stats(); stats.DecompressorCalls != 3 || stats.CacheHits != 1 {
			t.Fatalf("expected block one hit, got stats %+v", stats)
		}
		appendData(t, container, "g")
		readAndCheck(t, container, 4, 3, "efg", true)
		if got, _ := container.ReadAt(6, 1); string(got.Data) != "g" {
			t.Fatalf("rewritten final block returned %q", got.Data)
		}
	})
}

func appendData(t *testing.T, container *Container, data string) {
	t.Helper()
	if _, err := container.Append([]byte(data)); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptionDoesNotPolluteCache(t *testing.T) {
	scenarios := []struct {
		name       string
		direct     bool
		corrupt    func(record *blockRecord)
		kind       ErrorKind
		decompress Decompressor
	}{
		{"compressed decompression failure", false, func(*blockRecord) {}, Decompression, failDecompressor{}},
		{"compressed length mismatch", false, func(record *blockRecord) { record.origLength++ }, LengthMismatch, identityDecompressor{}},
		{"compressed checksum mismatch", false, func(record *blockRecord) { record.checksum++ }, ChecksumMismatch, identityDecompressor{}},
		{"direct length mismatch", true, func(record *blockRecord) { record.origLength++ }, LengthMismatch, identityDecompressor{}},
		{"direct checksum mismatch", true, func(record *blockRecord) { record.checksum++ }, ChecksumMismatch, identityDecompressor{}},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			script := &scriptCompressor{}
			var compressor Compressor = script
			if scenario.direct {
				script.failNext = true
			}
			if scenario.name == "compressed decompression failure" {
				compressor = fixedSizeCompressor{4}
			}
			minimumGain := 1
			if scenario.name == "compressed decompression failure" {
				minimumGain = 0
			}
			container, err := New(Config{BlockSize: 4, MinimumGain: minimumGain, CacheCapacity: 2}, compressor, scenario.decompress)
			if err != nil {
				t.Fatal(err)
			}
			appendData(t, container, "abcd")
			record := &container.blocks[0]
			scenario.corrupt(record)
			_, err = container.ReadAt(0, 4)
			var blockError BlockError
			if !errors.As(err, &blockError) || blockError.Kind != scenario.kind || blockError.BlockIndex != 0 {
				t.Fatalf("got %v, want %s block 0", err, scenario.kind)
			}
			if _, ok := container.cache.peek(0); ok {
				t.Fatal("failed read polluted cache")
			}
		})
	}
}

func TestDeterministicPhysicalEncoding(t *testing.T) {
	container := newTestContainer(t, 4, 0, 1)
	appendData(t, container, "abcd")
	record := container.blocks[0]
	header := encodeHeader(record)
	if len(header) != HeaderSize || StorageForm(header[0]) != FormCompressed {
		t.Fatalf("bad header: % x", header)
	}
	parsed := parseHeader(header)
	if parsed.origLength != 4 || parsed.checksum != crc32.ChecksumIEEE([]byte("abcd")) {
		t.Fatalf("bad parsed header: %+v", parsed)
	}
	if !strings.Contains(fmt.Sprintf("%v", BlockError{Kind: ChecksumMismatch, BlockIndex: 7}), "block 7") {
		t.Fatal("block error must identify block index")
	}
}

func TestConcurrentAppendVisibility(t *testing.T) {
	container := newTestContainer(t, 4, 100, 1)
	chunk := []byte("abcd")
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := container.Append(bytes.Clone(chunk)); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if container.Stats().LogicalBytes != 64 {
		t.Fatalf("unexpected logical length: %d", container.Stats().LogicalBytes)
	}
	result, err := container.ReadAt(0, 128)
	if err != nil || len(result.Data) != 64 {
		t.Fatalf("data=%d err=%v", len(result.Data), err)
	}
}
