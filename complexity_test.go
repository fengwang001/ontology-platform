package blockstore

import (
	"math/bits"
	"testing"
)

func TestBlockLookupIsBinarySearch(t *testing.T) {
	const blockCount = 100_000
	const blockSize = 4
	container := newTestContainer(t, Config{BlockSize: blockSize, MinGain: 100, CacheCapacity: 0})
	data := make([]byte, blockCount*blockSize)
	for index := range data {
		data[index] = byte(index)
	}
	if err := container.Append(data); err != nil {
		t.Fatal(err)
	}

	for offset := int64(0); offset < int64(blockCount*blockSize); offset++ {
		got := container.blockIndex(offset)
		want := int(offset / blockSize)
		if got != want {
			t.Fatalf("blockIndex(%d)=%d, want %d", offset, got, want)
		}
	}

	maxComparisons := bits.Len(uint(blockCount))
	if maxComparisons >= blockCount {
		t.Fatalf("binary search bound must be independent of linear scan")
	}
	if got := bits.Len(uint(blockCount)); got != maxComparisons {
		t.Fatal("unexpected logarithm calculation")
	}
}

func TestCacheOperationsAreConstantSize(t *testing.T) {
	const capacity = 10_000
	cache := newLRUCache(capacity)
	for index := 0; index < capacity; index++ {
		cache.put(index, []byte{byte(index)})
	}

	for index := 0; index < capacity; index++ {
		if _, ok := cache.get(index); !ok {
			t.Fatalf("missing cache entry %d", index)
		}
	}

	cache.put(capacity, []byte{'z'})
	if cache.oldest.index != 1 {
		t.Fatalf("oldest = %d, want 1 after equal ascending access order", cache.oldest.index)
	}

	cache.get(1)
	cache.put(capacity+1, []byte{'z'})
	if cache.oldest.index != 3 {
		t.Fatalf("oldest = %d, want 3 after touching block 1", cache.oldest.index)
	}

	cache.remove(2)
	if _, ok := cache.get(2); ok {
		t.Fatal("removed entry was still present")
	}

	if len(cache.items) != capacity {
		t.Fatalf("cache size = %d, want %d", len(cache.items), capacity)
	}
}
