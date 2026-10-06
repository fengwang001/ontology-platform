package blockstore

import (
	"errors"
	"testing"
)

func corruptContainer(t *testing.T, form StorageForm) *Container {
	t.Helper()
	minGain := 0
	compressor := Compressor(ruleCompressor{})
	if form == StoredRaw {
		minGain = 100
	} else {
		compressor = shortCodec{}
	}
	container, err := New(Config{BlockSize: 4, MinGain: minGain, CacheCapacity: 2}, compressor, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if form == StoredRaw {
		if err := container.Append([]byte("abcd")); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := container.Append([]byte("xxxx")); err != nil {
			t.Fatal(err)
		}
	}
	if got := headerForm(container.blocks[0]); got != form {
		t.Fatalf("setup form = %d, want %d", got, form)
	}
	return container
}

func assertReadError(t *testing.T, container *Container, kind ErrorKind) {
	t.Helper()
	_, err := container.ReadAt(0, 4)
	var blockErr *Error
	if !errors.As(err, &blockErr) {
		t.Fatalf("error = %v, want blockstore.Error", err)
	}
	if blockErr.Kind != kind || blockErr.BlockIndex != 0 {
		t.Fatalf("error kind=%d block=%d, want kind=%d block=0", blockErr.Kind, blockErr.BlockIndex, kind)
	}
}

func TestCorruptionOnRawAndCompressedBlocks(t *testing.T) {
	t.Run("raw decompression failure", func(t *testing.T) {
		container := corruptContainer(t, StoredRaw)
		container.blocks[0].physical[0] = byte(StoredCompressed)
		assertReadError(t, container, DecompressFailed)
	})

	t.Run("compressed decompression failure", func(t *testing.T) {
		container := corruptContainer(t, StoredCompressed)
		container.blocks[0].physical[headerSize] = '!'
		assertReadError(t, container, DecompressFailed)
	})

	t.Run("raw length mismatch", func(t *testing.T) {
		container := corruptContainer(t, StoredRaw)
		container.blocks[0].physical[16]++
		assertReadError(t, container, LengthMismatch)
	})

	t.Run("compressed length mismatch", func(t *testing.T) {
		container := corruptContainer(t, StoredCompressed)
		container.blocks[0].physical[16]++
		assertReadError(t, container, LengthMismatch)
	})

	t.Run("raw checksum mismatch", func(t *testing.T) {
		container := corruptContainer(t, StoredRaw)
		container.blocks[0].physical[headerSize] ^= 0xff
		assertReadError(t, container, ChecksumMismatch)
	})

	t.Run("compressed checksum mismatch", func(t *testing.T) {
		container := corruptContainer(t, StoredCompressed)
		container.blocks[0].physical[24] ^= 0xff
		assertReadError(t, container, ChecksumMismatch)
	})
}

func TestReadErrorDoesNotPolluteCache(t *testing.T) {
	container := corruptContainer(t, StoredCompressed)
	container.blocks[0].physical[headerSize] = '!'
	_, err := container.ReadAt(0, 4)
	if err == nil {
		t.Fatal("expected corruption error")
	}

	container.blocks[0].physical[headerSize] = 'x'
	result, err := container.ReadAt(0, 4)
	if err != nil {
		t.Fatalf("repair read failed: %v", err)
	}
	if string(result.Data) != "xxxx" {
		t.Fatalf("data = %q", result.Data)
	}
	stats := container.Stats()
	if stats.DecompressorCalls != 2 || stats.CacheHits != 0 {
		t.Fatalf("failed decode must not populate cache, stats=%+v", stats)
	}

	result, err = container.ReadAt(0, 4)
	if err != nil || string(result.Data) != "xxxx" {
		t.Fatalf("cached read data=%q err=%v", result.Data, err)
	}
	stats = container.Stats()
	if stats.DecompressorCalls != 2 || stats.CacheHits != 1 {
		t.Fatalf("successful decode should be cached, stats=%+v", stats)
	}
}

func TestErrorPriorityUsesEarliestKindAndBlock(t *testing.T) {
	container, err := New(Config{BlockSize: 4, MinGain: 0, CacheCapacity: 0}, shortCodec{}, shortCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Append([]byte("xxxxyyyy")); err != nil {
		t.Fatal(err)
	}
	container.blocks[0].physical[24] ^= 0xff
	container.blocks[1].physical[headerSize] = '!'
	_, readErr := container.ReadAt(0, 8)
	var blockErr *Error
	if !errors.As(readErr, &blockErr) || blockErr.Kind != ChecksumMismatch || blockErr.BlockIndex != 0 {
		t.Fatalf("error = %v, want checksum at block 0", readErr)
	}
}
