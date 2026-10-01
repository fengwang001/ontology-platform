package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestNewValidationPriority(t *testing.T) {
	_, err := New(0, 0)
	if !errors.Is(err, ErrInvalidPerKeyLimit) {
		t.Fatalf("expected per-key limit error, got %v", err)
	}

	_, err = New(1, 0)
	if !errors.Is(err, ErrInvalidGlobalLimit) {
		t.Fatalf("expected global limit error, got %v", err)
	}
}

func TestPutValidationPriority(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		key      string
		manifest []ReadItem
		result   string
		want     error
	}{
		{"empty key", "", manifest("a", "d"), "r", ErrEmptyKey},
		{"empty manifest", "k", nil, "r", ErrEmptyManifest},
		{"empty path before ordering", "k", []ReadItem{{Path: "b", Digest: "d"}, {Path: "", Digest: "d"}}, "r", ErrEmptyPath},
		{"equal paths", "k", []ReadItem{{Path: "a", Digest: "d"}, {Path: "a", Digest: "d"}}, "r", ErrPathsNotStrictlyOrdered},
		{"descending paths", "k", []ReadItem{{Path: "b", Digest: "d"}, {Path: "a", Digest: "d"}}, "r", ErrPathsNotStrictlyOrdered},
		{"empty digest", "k", []ReadItem{{Path: "a", Digest: ""}}, "r", ErrEmptyDigest},
		{"empty result", "k", manifest("a", "d"), "", ErrEmptyResult},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := cache.Put(tt.key, tt.manifest, tt.result); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}

	if got := cache.Len(); got != 0 {
		t.Fatalf("rejected puts changed Len: %d", got)
	}
	if got := len(cache.Dump("k")); got != 0 {
		t.Fatalf("rejected puts created entries: %d", got)
	}
}

func TestLookupEmptyKey(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	_, ok, err := cache.Lookup("", map[string]string{"a": "d"})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("expected empty key error, got %v", err)
	}
	if ok {
		t.Fatal("empty key lookup reported a hit")
	}
}

func TestSubsetMatchUsesNewestLast(t *testing.T) {
	cache, err := New(4, 10)
	if err != nil {
		t.Fatal(err)
	}

	short := []ReadItem{{Path: "a", Digest: "1"}}
	long := []ReadItem{{Path: "a", Digest: "1"}, {Path: "b", Digest: "2"}}

	if err := cache.Put("k", long, "long-first"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("k", short, "short-newer"); err != nil {
		t.Fatal(err)
	}

	got, ok, err := cache.Lookup("k", map[string]string{"a": "1", "b": "2", "c": "3"})
	if err != nil || !ok {
		t.Fatalf("expected hit, got ok=%v err=%v", ok, err)
	}
	if got != "short-newer" {
		t.Fatalf("expected newest last entry, got %q", got)
	}

	if err := cache.Put("k", long, "long-refreshed"); err != nil {
		t.Fatal(err)
	}
	got, ok, err = cache.Lookup("k", map[string]string{"a": "1", "b": "2"})
	if err != nil || !ok {
		t.Fatalf("expected refreshed long entry hit, got ok=%v err=%v", ok, err)
	}
	if got != "long-refreshed" {
		t.Fatalf("expected newest long entry, got %q", got)
	}
}

func TestDuplicateManifestReplacesResultAndRefreshesLast(t *testing.T) {
	cache, err := New(3, 10)
	if err != nil {
		t.Fatal(err)
	}

	first := manifest("a", "1")
	second := manifest("b", "2")
	if err := cache.Put("k", first, "first"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("k", second, "second"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("k", first, "first-replaced"); err != nil {
		t.Fatal(err)
	}

	if got := cache.Len(); got != 2 {
		t.Fatalf("expected duplicate put to keep Len 2, got %d", got)
	}

	dumped := cache.Dump("k")
	if len(dumped) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(dumped))
	}
	if dumped[0].Result != "first-replaced" || dumped[1].Result != "second" {
		t.Fatalf("expected replaced entry first, got %#v", dumped)
	}
}

func TestHitRefreshesLastChangesEviction(t *testing.T) {
	cache, err := New(2, 2)
	if err != nil {
		t.Fatal(err)
	}

	a := manifest("a", "1")
	b := manifest("b", "2")
	c := manifest("c", "3")

	if err := cache.Put("k", a, "a"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("other", b, "b"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := cache.Lookup("other", map[string]string{"b": "2"}); err != nil || !ok || got != "b" {
		t.Fatalf("lookup b = %q,%v,%v", got, ok, err)
	}
	if err := cache.Put("k", c, "c"); err != nil {
		t.Fatal(err)
	}

	if got := cache.Len(); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}
	if got, _, _ := cache.Lookup("k", map[string]string{"a": "1"}); got != "" {
		t.Fatalf("expected a evicted, got %q", got)
	}
	if got, ok, _ := cache.Lookup("other", map[string]string{"b": "2"}); !ok || got != "b" {
		t.Fatalf("expected refreshed b retained, got %q/%v", got, ok)
	}
}

func TestPerKeyEvictionRunsBeforeGlobalLimit(t *testing.T) {
	cache, err := New(2, 2)
	if err != nil {
		t.Fatal(err)
	}

	a := manifest("a", "1")
	b := manifest("b", "2")
	c := manifest("c", "3")

	if err := cache.Put("k", a, "a"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("other", b, "b"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("k", c, "c"); err != nil {
		t.Fatal(err)
	}

	if got := cache.Len(); got != 2 {
		t.Fatalf("expected two retained entries, got %d", got)
	}
	dumped := cache.Dump("k")
	if len(dumped) != 1 || dumped[0].Result != "c" {
		t.Fatalf("expected only c in k, got %#v", dumped)
	}
	if got := len(cache.Dump("other")); got != 1 {
		t.Fatalf("expected other untouched by per-key eviction")
	}
}

func TestMissingFileAndDigestMismatchAreMisses(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	if err := cache.Put("k", manifest("a", "1"), "result"); err != nil {
		t.Fatal(err)
	}

	for _, current := range []map[string]string{nil, {}, {"a": "2"}, {"other": "1"}} {
		if _, ok, err := cache.Lookup("k", current); err != nil || ok {
			t.Fatalf("current %#v: expected clean miss, got ok=%v err=%v", current, ok, err)
		}
	}

	dumped := cache.Dump("k")
	if len(dumped) != 1 || dumped[0].Last != 1 {
		t.Fatalf("misses must not refresh tick/last, got %#v", dumped)
	}
}

func TestRejectedPutDoesNotChangeTick(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("k", manifest("a", "1"), "a"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("", manifest("a", "1"), "bad"); err == nil {
		t.Fatal("expected empty key rejection")
	}

	dumped := cache.Dump("k")
	if len(dumped) != 1 || dumped[0].Last != 1 {
		t.Fatalf("rejected put changed tick/last, got %#v", dumped)
	}
}

func TestDefensiveCopies(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	input := manifest("a", "1")
	if err := cache.Put("k", input, "result"); err != nil {
		t.Fatal(err)
	}
	input[0].Path = "mutated"

	dumped := cache.Dump("k")
	dumped[0].Manifest[0].Digest = "mutated"

	got, ok, err := cache.Lookup("k", map[string]string{"a": "1"})
	if err != nil || !ok || got != "result" {
		t.Fatalf("mutation escaped: %q/%v/%v", got, ok, err)
	}
}

func TestDumpEmptyKeyAndUnknownKey(t *testing.T) {
	cache, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	if got := cache.Dump(""); len(got) != 0 {
		t.Fatalf("empty key dump = %#v", got)
	}
	if got := cache.Dump("missing"); len(got) != 0 {
		t.Fatalf("missing key dump = %#v", got)
	}
}

func TestLookupExaminedBoundAtScale(t *testing.T) {
	for _, keyCount := range []int{100, 10000} {
		t.Run(fmtKeyCount(keyCount), func(t *testing.T) {
			cache, err := New(4, keyCount*4)
			if err != nil {
				t.Fatal(err)
			}

			targetKey := "target"
			for i := 0; i < 4; i++ {
				item := ReadItem{Path: fmt.Sprintf("target-%d", i), Digest: "same"}
				if err := cache.Put(targetKey, []ReadItem{item}, fmt.Sprintf("target-%d", i)); err != nil {
					t.Fatal(err)
				}
			}

			for keyIndex := 0; keyIndex < keyCount-1; keyIndex++ {
				key := fmt.Sprintf("other-%d", keyIndex)
				for entryIndex := 0; entryIndex < 4; entryIndex++ {
					path := fmt.Sprintf("%s-%d", key, entryIndex)
					item := ReadItem{Path: path, Digest: "same"}
					if err := cache.Put(key, []ReadItem{item}, fmt.Sprintf("r-%d-%d", keyIndex, entryIndex)); err != nil {
						t.Fatal(err)
					}
				}
			}

			if got := cache.Len(); got != keyCount*4 {
				t.Fatalf("Len = %d, want %d", got, keyCount*4)
			}

			current := map[string]string{"target-0": "same"}
			if result, ok, err := cache.Lookup(targetKey, current); err != nil || !ok || result != "target-0" {
				t.Fatalf("target lookup = %q,%v,%v", result, ok, err)
			}

			cache.mu.Lock()
			examined := cache.examined
			cache.mu.Unlock()
			if examined > 4 {
				t.Fatalf("examined %d entries, want <= 4 across %d keys", examined, keyCount)
			}
		})
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	cache, err := New(4, 200)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for step := 0; step < 100; step++ {
				key := fmt.Sprintf("k%d", (worker+step)%12)
				path := fmt.Sprintf("p%d", step%4)
				manifest := []ReadItem{{Path: path, Digest: "d"}}
				if err := cache.Put(key, manifest, fmt.Sprintf("r-%d-%d", worker, step)); err != nil {
					t.Errorf("put: %v", err)
					return
				}
				if _, _, err := cache.Lookup(key, map[string]string{path: "d"}); err != nil {
					t.Errorf("lookup: %v", err)
					return
				}
				_ = cache.Dump(key)
				_ = cache.Len()
			}
		}(worker)
	}
	wait.Wait()

	if got := cache.Len(); got > 200 {
		t.Fatalf("Len = %d exceeds Cap", got)
	}
}

func manifest(pathDigest ...string) []ReadItem {
	if len(pathDigest)%2 != 0 {
		panic("manifest requires path/digest pairs")
	}

	result := make([]ReadItem, 0, len(pathDigest)/2)
	for i := 0; i < len(pathDigest); i += 2 {
		result = append(result, ReadItem{Path: pathDigest[i], Digest: pathDigest[i+1]})
	}
	return result
}

func fmtKeyCount(count int) string {
	return fmt.Sprintf("%d-keys", count)
}
