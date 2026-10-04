package docstore

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/shardmap"
)

func constHash(m map[string]uint32) HashFunc {
	return func(data []byte) uint32 {
		if v, ok := m[string(data)]; ok {
			return v
		}
		return uint32(len(data))
	}
}

func freshIndex(t *testing.T, name string, n, r, p int, h HashFunc) {
	t.Helper()
	var opts []shardmap.Option
	if h != nil {
		opts = append(opts, shardmap.WithHash(h))
	}
	if err := CreateIndex(name, n, r, p, opts...); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func TestSpecRoutingAndSplitExample(t *testing.T) {
	freshIndex(t, "spec", 2, 8, 1, constHash(map[string]uint32{"id": 13}))
	if err := Put("spec", []byte("id"), []byte("id"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	counts, err := Count("spec")
	if err != nil || len(counts) != 2 || counts[1] != 1 {
		t.Fatalf("counts=%v err=%v, want shard 1", counts, err)
	}
	if err := SetWriteBlock("spec", true); err != nil {
		t.Fatal(err)
	}
	if err := Split("spec", 4); err != nil {
		t.Fatalf("split to 4: %v", err)
	}
	counts, _ = Count("spec")
	if len(counts) != 4 || counts[2] != 1 {
		t.Fatalf("after split 4 counts=%v, want shard 2 (parent 1)", counts)
	}
	if err := Split("spec", 8); err != nil {
		t.Fatalf("split to 8: %v", err)
	}
	counts, _ = Count("spec")
	if len(counts) != 8 || counts[5] != 1 {
		t.Fatalf("after split 8 counts=%v, want shard 5", counts)
	}
}

func TestIllegalSplitThreeRules(t *testing.T) {
	freshIndex(t, "illegal-split", 2, 8, 1, nil)
	if err := SetWriteBlock("illegal-split", true); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n2      int
		wantErr error
	}{
		{3, ErrCannotSplit},
		{16, ErrCannotSplit},
		{2, ErrCannotSplit},
		{0, ErrInvalidParam},
		{1025, ErrInvalidParam},
	}
	for _, tc := range cases {
		if err := Split("illegal-split", tc.n2); !errors.Is(err, tc.wantErr) {
			t.Fatalf("Split(%d) err=%v want %v", tc.n2, err, tc.wantErr)
		}
	}
	counts, _ := Count("illegal-split")
	if len(counts) != 2 {
		t.Fatalf("N changed after rejected splits: %d shards", len(counts))
	}
}

func TestSearchShardsWrapAround(t *testing.T) {
	freshIndex(t, "wrap", 4, 8, 3, constHash(map[string]uint32{"r": 6, "x": 5, "y": 3}))
	got, err := SearchShards("wrap", []byte("r"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("search shards=%v want [0 3]", got)
	}
	if err := Put("wrap", []byte("x"), []byte("r"), nil); err != nil {
		t.Fatal(err)
	}
	if err := Put("wrap", []byte("y"), []byte("r"), nil); err != nil {
		t.Fatal(err)
	}
	counts, _ := Count("wrap")
	if counts[0] != 1 || counts[3] != 1 {
		t.Fatalf("counts=%v want x->0, y->3", counts)
	}
	if err := SetWriteBlock("wrap", true); err != nil {
		t.Fatal(err)
	}
	// Shrink 到 2：2 不大于 P=3，报不可收缩。
	if err := Shrink("wrap", 2); !errors.Is(err, ErrCannotShrink) {
		t.Fatalf("shrink with P=3 to 2: %v", err)
	}
}

func TestShrinkIDConflictRejectsAtomically(t *testing.T) {
	freshIndex(t, "conflict", 4, 8, 1, constHash(map[string]uint32{"k": 7, "r1": 1, "r3": 3}))
	if err := Put("conflict", []byte("k"), []byte("r1"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := Put("conflict", []byte("k"), []byte("r3"), []byte("v3")); err != nil {
		t.Fatal(err)
	}
	counts, _ := Count("conflict")
	if counts[0] != 1 || counts[1] != 1 {
		t.Fatalf("counts=%v want shards 0 and 1", counts)
	}
	if err := SetWriteBlock("conflict", true); err != nil {
		t.Fatal(err)
	}
	err := Shrink("conflict", 2)
	if !errors.Is(err, ErrIDConflict) || !strings.Contains(err.Error(), "k") {
		t.Fatalf("shrink err=%v want id conflict k", err)
	}
	counts, _ = Count("conflict")
	if len(counts) != 4 || counts[0] != 1 || counts[1] != 1 {
		t.Fatalf("state changed after rejected shrink: %v", counts)
	}
	if err := SetWriteBlock("conflict", false); err != nil {
		t.Fatal(err)
	}
	if err := Delete("conflict", []byte("k"), []byte("r3")); err != nil {
		t.Fatal(err)
	}
	if err := SetWriteBlock("conflict", true); err != nil {
		t.Fatal(err)
	}
	if err := Shrink("conflict", 2); err != nil {
		t.Fatalf("shrink after delete: %v", err)
	}
	body, err := Get("conflict", []byte("k"), []byte("r1"))
	if err != nil || string(body) != "v1" {
		t.Fatalf("get after shrink body=%q err=%v", body, err)
	}
}

func TestSameIDDifferentRoutingCoexist(t *testing.T) {
	freshIndex(t, "coexist", 4, 8, 1, constHash(map[string]uint32{"k": 7, "r1": 1, "r3": 3}))
	if err := Put("coexist", []byte("k"), []byte("r1"), []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := Put("coexist", []byte("k"), []byte("r3"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if body, err := Get("coexist", []byte("k"), []byte("r1")); err != nil || string(body) != "a" {
		t.Fatalf("r1 body=%q err=%v", body, err)
	}
	if body, err := Get("coexist", []byte("k"), []byte("r3")); err != nil || string(body) != "b" {
		t.Fatalf("r3 body=%q err=%v", body, err)
	}
	// Get/Delete 不跨分片：删 r1 后 r3 副本仍在。
	if err := Delete("coexist", []byte("k"), []byte("r1")); err != nil {
		t.Fatal(err)
	}
	if _, err := Get("coexist", []byte("k"), []byte("r1")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("deleted get err=%v want not found", err)
	}
	if body, err := Get("coexist", []byte("k"), []byte("r3")); err != nil || string(body) != "b" {
		t.Fatalf("r3 should survive: body=%q err=%v", body, err)
	}
}

func TestWriteBlockAndMissingRouting(t *testing.T) {
	freshIndex(t, "block", 4, 8, 3, constHash(nil))
	if err := Put("block", []byte("id"), nil, nil); !errors.Is(err, ErrMissingRouting) {
		t.Fatalf("put without routing P>1 err=%v", err)
	}
	if _, err := Get("block", []byte("id"), nil); !errors.Is(err, ErrMissingRouting) {
		t.Fatalf("get without routing P>1 err=%v", err)
	}
	if _, err := SearchShards("block", nil); !errors.Is(err, ErrMissingRouting) {
		t.Fatalf("search without routing err=%v", err)
	}
	if err := SetWriteBlock("block", true); err != nil {
		t.Fatal(err)
	}
	if err := Put("block", []byte("id"), []byte("r"), nil); !errors.Is(err, ErrIndexReadOnly) {
		t.Fatalf("blocked put err=%v", err)
	}
	if err := Delete("block", []byte("id"), []byte("r")); !errors.Is(err, ErrIndexReadOnly) {
		t.Fatalf("blocked delete err=%v", err)
	}
	// 只读判定先于缺少路由。
	if err := Put("block", []byte("id"), nil, nil); !errors.Is(err, ErrIndexReadOnly) {
		t.Fatalf("blocked+missing routing err=%v want read-only", err)
	}
	// Get 与 SearchShards 在阻塞期仍允许。
	if _, err := Get("block", []byte("id"), []byte("r")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("blocked get err=%v", err)
	}
	if _, err := SearchShards("block", []byte("r")); err != nil {
		t.Fatalf("blocked search err=%v", err)
	}
	// 解除阻塞后 Split/Shrink 报未置写阻塞。
	if err := SetWriteBlock("block", false); err != nil {
		t.Fatal(err)
	}
	if err := Split("block", 8); !errors.Is(err, ErrWriteBlockRequired) {
		t.Fatalf("unblocked split err=%v", err)
	}
	if err := Shrink("block", 2); !errors.Is(err, ErrWriteBlockRequired) {
		t.Fatalf("unblocked shrink err=%v", err)
	}
	// 参数非法先于索引不存在。
	if err := Split("nope", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("invalid n2 on missing index err=%v", err)
	}
	if err := Split("nope", 8); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("split missing index err=%v", err)
	}
	if err := Put("nope", []byte("id"), []byte("r"), nil); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("put missing index err=%v", err)
	}
	if err := CreateIndex("block", 4, 8, 3); !errors.Is(err, ErrIndexExists) {
		t.Fatalf("duplicate err=%v", err)
	}
}

func TestInvalidCreateParams(t *testing.T) {
	cases := []struct{ n, r, p int }{
		{0, 8, 1}, {1025, 2048, 1}, {4, 2, 1}, {3, 8, 1}, {2, 1<<20 + 2, 1}, {4, 8, 4}, {2, 8, 2},
	}
	for i, tc := range cases {
		if err := CreateIndex(fmt.Sprintf("bad-%d", i), tc.n, tc.r, tc.p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: err=%v want invalid", i, err)
		}
	}
}

func TestConcurrentLinearizable(t *testing.T) {
	freshIndex(t, "conc", 4, 16, 1, nil)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				id := []byte(fmt.Sprintf("w%d-k%d", w, k))
				if err := Put("conc", id, id, []byte("v")); err != nil {
					t.Errorf("put: %v", err)
					return
				}
				if _, err := Get("conc", id, id); err != nil {
					t.Errorf("get: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	counts, err := Count("conc")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 8*200 {
		t.Fatalf("total=%d want %d", total, 8*200)
	}
}

func TestConcurrentResizeVersusIO(t *testing.T) {
	freshIndex(t, "conc-resize", 16, 4096, 1, nil)
	for i := 0; i < 3000; i++ {
		id := []byte(fmt.Sprintf("d-%05d", i))
		if err := Put("conc-resize", id, id, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup

	// 分裂/收缩循环（N 在 16/32/.../256 与回 16 之间）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		n2s := []int{32, 64, 128, 256, 128, 64, 32, 16}
		for round := 0; round < 3; round++ {
			for _, n2 := range n2s {
				if err := SetWriteBlock("conc-resize", true); err != nil {
					t.Error(err)
					return
				}
				current, _ := Count("conc-resize")
				var err error
				if n2 > len(current) {
					err = Split("conc-resize", n2)
				} else if n2 < len(current) {
					err = Shrink("conc-resize", n2)
				}
				if err != nil {
					t.Error(err)
					return
				}
				if err := SetWriteBlock("conc-resize", false); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()

	// 读写并发。
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 2000; k++ {
				id := []byte(fmt.Sprintf("d-%05d", (seed*2000+k)%3000))
				if _, err := Get("conc-resize", id, id); err != nil &&
					!errors.Is(err, ErrDocumentNotFound) && !errors.Is(err, ErrIndexReadOnly) {
					t.Errorf("get: %v", err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
}
