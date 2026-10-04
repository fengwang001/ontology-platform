package docstore

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/shardmap"
	"ontology/slot"
)

// smallHash 把字节串映射到很小的值域以制造碰撞：
// "idN"/"rtN" -> N%hashMod，其余按 FNV 折回。
func smallHash(hashMod uint32) slot.HashFunc {
	return func(b []byte) uint32 {
		s := string(b)
		if strings.HasPrefix(s, "id") || strings.HasPrefix(s, "rt") {
			var n uint32
			for _, c := range s[2:] {
				if c >= '0' && c <= '9' {
					n = n*10 + uint32(c-'0')
				}
			}
			return n % hashMod
		}
		return slot.FNV32(b) % hashMod
	}
}

func createIdx(t *testing.T, store *Store, name string, n, r, p int, h slot.HashFunc) {
	t.Helper()
	if err := shardmap.CreateIndex(name, n, r, p, h); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
}

func TestPutGetDeleteSingleShard(t *testing.T) {
	store := New()
	createIdx(t, store, "i", 2, 8, 1, smallHash(8))
	if err := store.Put("i", []byte("id1"), []byte("rt1"), []byte("body1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("i", []byte("id1"), []byte("rt1"), []byte("body2")); err != nil {
		t.Fatal(err)
	}
	body, err := store.Get("i", []byte("id1"), []byte("rt1"))
	if err != nil || string(body) != "body2" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	// rt77 与 rt1 落不同分片（77%8=5 vs 1%8=1），且 id1 只在 rt1 的分片
	if _, err := store.Get("i", []byte("id1"), []byte("rt77")); !errors.Is(err, ErrDocumentMissing) {
		t.Fatalf("err=%v want document missing", err)
	}
	if err := store.Delete("i", []byte("id1"), []byte("rt1")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("i", []byte("id1"), []byte("rt1")); !errors.Is(err, ErrDocumentMissing) {
		t.Fatalf("err=%v", err)
	}
}

func TestSameIDDifferentRoutingCoexist(t *testing.T) {
	store := New()
	n, r := 4, 8
	createIdx(t, store, "co", n, r, 1, smallHash(8))
	var a, b []byte
	p := slot.Params{N: n, R: r, P: 1, H: smallHash(8)}
	for x := 0; x < 8 && b == nil; x++ {
		for y := x + 1; y < 8; y++ {
			ra := []byte(fmt.Sprintf("rt%d", x))
			rb := []byte(fmt.Sprintf("rt%d", y))
			if p.Shard([]byte("idk"), ra) != p.Shard([]byte("idk"), rb) {
				a, b = ra, rb
				break
			}
		}
	}
	if b == nil {
		t.Fatal("no routing pair found")
	}
	if err := store.Put("co", []byte("idk"), a, []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("co", []byte("idk"), b, []byte("B")); err != nil {
		t.Fatal(err)
	}
	gotA, err := store.Get("co", []byte("idk"), a)
	if err != nil || string(gotA) != "A" {
		t.Fatalf("A: %q %v", gotA, err)
	}
	gotB, err := store.Get("co", []byte("idk"), b)
	if err != nil || string(gotB) != "B" {
		t.Fatalf("B: %q %v", gotB, err)
	}
	counts, _ := store.Count("co")
	if sumCounts(counts) != 2 {
		t.Fatalf("total=%d want 2", sumCounts(counts))
	}

	shardmap.SetWriteBlock("co", true)
	shrinkMerge := p.Shard([]byte("idk"), a)/2 == p.Shard([]byte("idk"), b)/2
	err = shardmap.Shrink("co", n/2)
	if shrinkMerge {
		if !errors.Is(err, shardmap.ErrIDConflict) {
			t.Fatalf("want id conflict, got %v", err)
		}
		var ce *shardmap.IDConflictError
		if !errors.As(err, &ce) || string(ce.ID) != "idk" {
			t.Fatalf("conflict id=%v want idk", ce)
		}
		counts2, _ := store.Count("co")
		if len(counts2) != n || sumCounts(counts2) != 2 {
			t.Fatalf("state changed after rejected shrink: %v", counts2)
		}
		shardmap.SetWriteBlock("co", false)
		if err := store.Delete("co", []byte("idk"), a); err != nil {
			t.Fatal(err)
		}
		shardmap.SetWriteBlock("co", true)
		if err := shardmap.Shrink("co", n/2); err != nil {
			t.Fatal(err)
		}
		shardmap.SetWriteBlock("co", false)
		body, err := store.Get("co", []byte("idk"), b)
		if err != nil || string(body) != "B" {
			t.Fatalf("after shrink get: %q %v", body, err)
		}
	} else if err != nil {
		t.Fatalf("unexpected shrink err: %v", err)
	}
}

func sumCounts(c []int) int {
	sum := 0
	for _, v := range c {
		sum += v
	}
	return sum
}

func TestWriteBlockRejectsBothWays(t *testing.T) {
	store := New()
	createIdx(t, store, "wb", 2, 8, 1, smallHash(8))
	if err := store.Put("wb", []byte("id1"), []byte("rt1"), nil); err != nil {
		t.Fatal(err)
	}
	if err := shardmap.SetWriteBlock("wb", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("wb", []byte("id2"), []byte("rt2"), nil); !errors.Is(err, shardmap.ErrReadOnly) {
		t.Fatalf("put err=%v", err)
	}
	if err := store.Delete("wb", []byte("id1"), []byte("rt1")); !errors.Is(err, shardmap.ErrReadOnly) {
		t.Fatalf("delete err=%v", err)
	}
	if _, err := store.Get("wb", []byte("id1"), []byte("rt1")); err != nil {
		t.Fatalf("get during block: %v", err)
	}
	store2 := New()
	createIdx(t, store2, "wb2", 4, 8, 1, smallHash(8))
	if err := shardmap.Split("wb2", 8); !errors.Is(err, shardmap.ErrNotWriteBlocked) {
		t.Fatalf("split err=%v", err)
	}
	if err := shardmap.Shrink("wb2", 2); !errors.Is(err, shardmap.ErrNotWriteBlocked) {
		t.Fatalf("shrink err=%v", err)
	}
}

func TestMissingRouting(t *testing.T) {
	store := New()
	createIdx(t, store, "mr", 4, 8, 3, smallHash(8))
	if err := store.Put("mr", []byte("id1"), nil, nil); !errors.Is(err, shardmap.ErrMissingRouting) {
		t.Fatalf("put err=%v", err)
	}
	if err := store.Put("mr", []byte("id1"), []byte("rt1"), nil); err != nil {
		t.Fatalf("put with routing: %v", err)
	}
	if _, err := store.Get("mr", []byte("id1"), nil); !errors.Is(err, shardmap.ErrMissingRouting) {
		t.Fatalf("get err=%v", err)
	}
	if err := store.Delete("mr", []byte("id1"), nil); !errors.Is(err, shardmap.ErrMissingRouting) {
		t.Fatalf("delete err=%v", err)
	}
}

func TestSplitExampleNoCrossParent(t *testing.T) {
	store := New()
	h := func(b []byte) uint32 {
		if string(b) == "rt" {
			return 13
		}
		return smallHash(8)(b)
	}
	createIdx(t, store, "ex", 2, 8, 1, h)
	if err := store.Put("ex", []byte("d"), []byte("rt"), []byte("z")); err != nil {
		t.Fatal(err)
	}
	counts, _ := store.Count("ex")
	if counts[1] != 1 || counts[0] != 0 {
		t.Fatalf("counts=%v want doc on shard 1", counts)
	}
	shardmap.SetWriteBlock("ex", true)
	if err := shardmap.Split("ex", 4); err != nil {
		t.Fatal(err)
	}
	counts4, _ := store.Count("ex")
	if counts4[2] != 1 {
		t.Fatalf("after split counts=%v want doc on shard 2", counts4)
	}
	if err := shardmap.Split("ex", 8); err != nil {
		t.Fatal(err)
	}
	counts8, _ := store.Count("ex")
	if counts8[5] != 1 {
		t.Fatalf("after split8 counts=%v want doc on shard 5", counts8)
	}
	shardmap.SetWriteBlock("ex", false)
	if body, err := store.Get("ex", []byte("d"), []byte("rt")); err != nil || string(body) != "z" {
		t.Fatalf("get after splits: %q %v", body, err)
	}
}

func TestGetTouchesIndependentOfSize(t *testing.T) {
	for _, total := range []int{100, 100000} {
		store := New()
		name := fmt.Sprintf("big%d", total)
		createIdx(t, store, name, 16, 256, 1, smallHash(64))
		for i := 0; i < total; i++ {
			id := []byte(fmt.Sprintf("id%d", i))
			rt := []byte(fmt.Sprintf("rt%d", i))
			if err := store.Put(name, id, rt, []byte("v")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.Get(name, []byte("id37"), []byte("rt37")); err != nil {
			t.Fatal(err)
		}
		touches := store.LastGetTouches()
		if touches.ShardsTouched != 1 || touches.RecordsSeen > 1 {
			t.Fatalf("total=%d touches=%+v want 1 shard, <=1 record", total, touches)
		}
		_, _ = store.Get(name, []byte("id37"), []byte("rt0"))
		touches = store.LastGetTouches()
		if touches.ShardsTouched != 1 || touches.RecordsSeen != 0 {
			t.Fatalf("miss total=%d touches=%+v want 1 shard, 0 record", total, touches)
		}
	}
}

func TestInvalidArgsDocstore(t *testing.T) {
	store := New()
	createIdx(t, store, "ia", 2, 8, 1, smallHash(8))
	long := bytes.Repeat([]byte("x"), 513)
	if err := store.Put("ia", nil, []byte("rt"), nil); !errors.Is(err, shardmap.ErrInvalidArgument) {
		t.Fatalf("nil id err=%v", err)
	}
	if err := store.Put("ia", long, []byte("rt"), nil); !errors.Is(err, shardmap.ErrInvalidArgument) {
		t.Fatalf("long id err=%v", err)
	}
	if err := store.Put("ia", []byte("id"), long, nil); !errors.Is(err, shardmap.ErrInvalidArgument) {
		t.Fatalf("long routing err=%v", err)
	}
	if _, err := store.Get("ghost", []byte("id"), []byte("rt")); !errors.Is(err, shardmap.ErrNotFound) {
		t.Fatalf("missing index err=%v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store := New()
	createIdx(t, store, "cc", 8, 64, 1, smallHash(32))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				k := base*200 + j
				id := []byte(fmt.Sprintf("id%d", k))
				rt := []byte(fmt.Sprintf("rt%d", k))
				_ = store.Put("cc", id, rt, []byte("b"))
				_, _ = store.Get("cc", id, rt)
			}
		}(i)
	}
	wg.Wait()
	counts, err := store.Count("cc")
	if err != nil {
		t.Fatal(err)
	}
	if sumCounts(counts) != 1600 {
		t.Fatalf("total=%d want 1600", sumCounts(counts))
	}
}
