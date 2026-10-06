package blockstore

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type ruleCompressor struct {
	failOn map[string]bool
}

func (r ruleCompressor) Compress(data []byte) ([]byte, bool) {
	if r.failOn[string(data)] {
		return nil, false
	}
	return append([]byte{'C'}, data...), true
}

type prefixDecompressor struct{}

func (prefixDecompressor) Decompress(data []byte) ([]byte, bool) {
	if len(data) == 0 || data[0] != 'C' {
		return nil, false
	}
	return append([]byte(nil), data[1:]...), true
}

type shortCodec struct{}

var shortPayloads = map[string]string{
	"ab":   "a",
	"abc":  "t",
	"cd":   "c",
	"xxxx": "x",
	"xx":   "y",
	"xxx":  "z",
	"yyy":  "yy",
	"zzz":  "qqq",
	"ww":   "w",
}

func (shortCodec) Compress(data []byte) ([]byte, bool) {
	if encoded, ok := shortPayloads[string(data)]; ok {
		return []byte(encoded), true
	}
	return append([]byte{'C'}, data...), true
}

func (shortCodec) Decompress(data []byte) ([]byte, bool) {
	for original, encoded := range shortPayloads {
		if encoded == string(data) {
			return []byte(original), true
		}
	}
	if len(data) != 0 && data[0] == 'C' {
		return data[1:], true
	}
	return nil, false
}

func newTestContainer(t *testing.T, config Config) *Container {
	t.Helper()
	container, err := New(config, ruleCompressor{}, prefixDecompressor{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return container
}

func TestCompressionThresholds(t *testing.T) {
	container := newTestContainer(t, Config{BlockSize: 10, MinGain: 2, CacheCapacity: 2})
	if err := container.Append([]byte("ab")); err != nil {
		t.Fatal(err)
	}

	stats := container.Stats()
	if stats.CompressedBlocks != 0 || stats.InsufficientGainBlocks != 1 {
		t.Fatalf("original <= min gain: stats = %+v", stats)
	}

	container, err := New(Config{BlockSize: 10, MinGain: 2, CacheCapacity: 2}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("xxx")); err != nil {
		t.Fatal(err)
	}
	if got := container.blocks[0]; headerForm(got) != StoredCompressed || headerStoredLength(got) != 1 {
		t.Fatalf("compressed length = original-minGain+1, got form %d len %d", headerForm(got), headerStoredLength(got))
	}
	if got := container.blocks[0].reason; got != NoFallback {
		t.Fatalf("equal threshold should be compressed, reason=%d", got)
	}

	container, err = New(Config{BlockSize: 10, MinGain: 1, CacheCapacity: 2}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("zzz")); err != nil {
		t.Fatal(err)
	}
	if got := container.blocks[0]; headerForm(got) != StoredRaw || headerStoredLength(got) != 3 {
		t.Fatalf("compressed length = original-minGain+1 should be raw, got form %d len %d", headerForm(got), headerStoredLength(got))
	}
}

func TestFallbackReasonsAndCompressorCallCount(t *testing.T) {
	container, err := New(Config{BlockSize: 2, MinGain: 10, CacheCapacity: 1},
		ruleCompressor{failOn: map[string]bool{"ab": true}}, prefixDecompressor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("abcd")); err != nil {
		t.Fatal(err)
	}

	if container.blocks[0].reason != CompressionFailed || container.blocks[1].reason != InsufficientGain {
		t.Fatalf("reasons = %d, %d", container.blocks[0].reason, container.blocks[1].reason)
	}
	stats := container.Stats()
	if stats.CompressorCalls != 2 || stats.CompressionFailedBlocks != 1 || stats.InsufficientGainBlocks != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestAppendRewritesLastBlockAndRecompressesOnce(t *testing.T) {
	container := newTestContainer(t, Config{BlockSize: 4, MinGain: 100, CacheCapacity: 1})
	if err := container.Append([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	result, err := container.ReadAt(0, 2)
	if err != nil || string(result.Data) != "ab" {
		t.Fatalf("initial read result=%q err=%v", result.Data, err)
	}

	if err := container.Append([]byte("cdefg")); err != nil {
		t.Fatal(err)
	}
	result, err = container.ReadAt(0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "abcdefg" || !result.EOF {
		t.Fatalf("read = %q eof=%v", result.Data, result.EOF)
	}

	stats := container.Stats()
	if stats.CompressorCalls != 3 || stats.DecompressorCalls != 0 || stats.CacheHits != 0 {
		t.Fatalf("rewrite should invalidate cache before read, stats=%+v", stats)
	}
}

func TestReadBoundariesAndCrossBlockRanges(t *testing.T) {
	container := newTestContainer(t, Config{BlockSize: 4, MinGain: 100})
	if err := container.Append([]byte("abcdefghij")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		offset int64
		length int64
		want   string
		eof    bool
	}{
		{"zero length at boundary", 4, 0, "", false},
		{"offset equals logical length", 10, 0, "", true},
		{"clamped to end", 7, 10, "hij", true},
		{"crosses blocks", 2, 5, "cdefg", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := container.ReadAt(tt.offset, tt.length)
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Data) != tt.want || result.EOF != tt.eof {
				t.Fatalf("got %q eof=%v, want %q eof=%v", result.Data, result.EOF, tt.want, tt.eof)
			}
		})
	}

	_, err := container.ReadAt(11, 1)
	var blockErr *Error
	if !errors.As(err, &blockErr) || blockErr.Kind != OutOfRange {
		t.Fatalf("error = %v, want out of range", err)
	}
}

func TestCacheZeroAndOne(t *testing.T) {
	container, err := New(Config{BlockSize: 2, MinGain: 0, CacheCapacity: 0}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	_, _ = container.ReadAt(0, 4)
	_, _ = container.ReadAt(0, 4)
	if stats := container.Stats(); stats.CacheHits != 0 || stats.DecompressorCalls != 4 {
		t.Fatalf("zero cache stats=%+v", stats)
	}

	container, err = New(Config{BlockSize: 2, MinGain: 0, CacheCapacity: 1}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	_, _ = container.ReadAt(0, 2)
	_, _ = container.ReadAt(2, 2)
	stats := container.Stats()
	if stats.CacheHits != 0 || stats.DecompressorCalls != 2 {
		t.Fatalf("capacity one after fills stats=%+v", stats)
	}

	_, _ = container.ReadAt(2, 2)
	stats = container.Stats()
	if stats.CacheHits != 1 || stats.DecompressorCalls != 2 {
		t.Fatalf("repeat newest stats=%+v", stats)
	}

	_, _ = container.ReadAt(0, 2)
	_, _ = container.ReadAt(2, 2)
	stats = container.Stats()
	if stats.CacheHits != 1 || stats.DecompressorCalls != 4 {
		t.Fatalf("evict and refill stats=%+v", stats)
	}
}

func TestRewriteInvalidatesCachedOldTail(t *testing.T) {
	container := newTestContainer(t, Config{BlockSize: 4, MinGain: 100, CacheCapacity: 2})
	if err := container.Append([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	old, _ := container.ReadAt(0, 2)
	if string(old.Data) != "ab" {
		t.Fatalf("old = %q", old.Data)
	}
	if err := container.Append([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	result, err := container.ReadAt(0, 4)
	if err != nil || string(result.Data) != "abcd" {
		t.Fatalf("after rewrite data=%q err=%v", result.Data, err)
	}
}

func TestTailRewriteCacheLifecycle(t *testing.T) {
	container, err := New(Config{BlockSize: 4, MinGain: 0, CacheCapacity: 2}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("xxx")); err != nil {
		t.Fatal(err)
	}
	if _, err := container.ReadAt(0, 3); err != nil {
		t.Fatal(err)
	}
	if _, ok := container.cache.peek(0); !ok {
		t.Fatal("short compressed tail should be cached")
	}

	if err := container.Append([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if _, ok := container.cache.peek(0); ok {
		t.Fatal("cache entry for completed tail must be invalidated")
	}

	if err := container.Append([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := container.ReadAt(4, 2); err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("c")); err != nil {
		t.Fatal(err)
	}
	cached, ok := container.cache.peek(1)
	if !ok || string(cached) != "abc" {
		t.Fatalf("unfinished rewritten tail cache = %q, ok=%v, want abc", cached, ok)
	}
	result, err := container.ReadAt(0, 8)
	if err != nil || string(result.Data) != "xxxyabc" {
		t.Fatalf("data=%q err=%v, want xxxyabc", result.Data, err)
	}
}

func TestConcurrentAppendAndReadAreAtomic(t *testing.T) {
	container := newTestContainer(t, Config{BlockSize: 7, MinGain: 100, CacheCapacity: 3})
	var wait sync.WaitGroup
	for round := 0; round < 8; round++ {
		chunk := []byte(fmt.Sprintf("chunk%02d", round))
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := container.Append(chunk); err != nil {
				t.Error(err)
			}
		}()
	}

	for reader := 0; reader < 4; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				length := container.Stats().LogicalBytes
				result, err := container.ReadAt(0, length)
				if err != nil {
					t.Error(err)
					return
				}
				if len(result.Data) != int(length) {
					t.Errorf("partial read: got %d, want %d", len(result.Data), length)
					return
				}
				if length == 56 {
					return
				}
			}
		}()
	}
	wait.Wait()
}
