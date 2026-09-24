package chunk

import (
	"math/rand"
	"testing"

	"ontology/verify"
)

// TestRollingEveryOffset 在随机数据上对每个偏移断言滚动值==从头重算值。
func TestRollingEveryOffset(t *testing.T) {
	cases := []struct {
		name string
		len  int
		n    uint32
		seed int64
	}{
		{"short", 5, 1, 7},
		{"exact", 16, 4, 8},
		{"random", 203, 4, 9},
		{"bigger-block", 177, 7, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.len)
			r := rand.New(rand.NewSource(tc.seed))
			r.Read(data)
			sc, err := NewScanner(data, tc.n)
			if err != nil {
				t.Fatal(err)
			}
			if !sc.Valid() {
				t.Fatalf("expected valid first window")
			}
			for off := 0; off+int(tc.n) <= len(data); off++ {
				if sc.Pos() != off {
					t.Fatalf("pos=%d want %d", sc.Pos(), off)
				}
				if got, want := sc.Weak(), Weak(data[off:off+int(tc.n)]); got != want {
					t.Fatalf("offset %d weak=%08x recompute=%08x", off, got, want)
				}
				if off+int(tc.n) < len(data) {
					sc.Advance()
				}
			}
		})
	}
}

// TestWeakCollisionCaughtByStrong 构造弱和相同但内容不同的块，强和必须能区分。
func TestWeakCollisionCaughtByStrong(t *testing.T) {
	cases := []struct {
		name string
		x, y []byte
	}{
		{"crafted", []byte{221, 251, 75, 153}, []byte{229, 214, 125, 132}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if Weak(tc.x) != Weak(tc.y) {
				t.Fatalf("precondition: weak must collide")
			}
			if Strong(tc.x) == Strong(tc.y) {
				t.Fatalf("strong hash must distinguish colliding blocks")
			}
		})
	}
}

// TestWeakOpBound 断言滚动基本运算次数不超过 4*数据长度。
func TestWeakOpBound(t *testing.T) {
	cases := []struct {
		name string
		len  int
		n    uint32
	}{
		{"unit-block", 300, 1}, {"normal", 300, 4}, {"large-block", 300, 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.len)
			rand.New(rand.NewSource(1)).Read(data)
			sc, _ := NewScanner(data, tc.n)
			for sc.Valid() {
				sc.Advance()
			}
			if got, limit := sc.WeakOps(), 4*len(data); got > limit {
				t.Fatalf("weakOps=%d > 4L=%d", got, limit)
			}
		})
	}
}

// TestStrongLeqWeakHits 强和只在弱命中时计算：次数不超过弱命中次数。
func TestStrongLeqWeakHits(t *testing.T) {
	data := make([]byte, 197)
	rand.New(rand.NewSource(3)).Read(data)
	target := append([]byte{}, data...)
	const n uint32 = 4
	blocks, _ := Split(target, n)
	table := map[uint32][]Block{}
	for _, blk := range blocks {
		table[blk.Weak] = append(table[blk.Weak], blk)
	}
	sc, _ := NewScanner(data, n)
	for sc.Valid() {
		if cand, ok := table[sc.Weak()]; ok {
			sc.MarkWeakHit()
			w := sc.ConfirmStrong()
			found := false
			for _, blk := range cand {
				if blk.Strong == w {
					found = true
				}
			}
			if !found {
				t.Fatalf("strong confirm unexpectedly failed")
			}
		}
		sc.Advance()
	}
	if sc.StrongCalls() > sc.WeakHits() {
		t.Fatalf("strong=%d weakHits=%d", sc.StrongCalls(), sc.WeakHits())
	}
	if sc.WeakHits() == 0 {
		t.Fatalf("expected weak hits")
	}
}

// TestSplitBoundaries 覆盖块大小 0、块大于数据、整数倍/非整数倍与末块不满。
func TestSplitBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		n       uint32
		wantErr error
		blocks  int
		lastLen int
	}{
		{"zero-block", []byte{1, 2}, 0, verify.ErrBadBlockSize, 0, 0},
		{"block-bigger", []byte{1, 2}, 8, nil, 1, 2},
		{"exact-multiple", []byte{1, 2, 3, 4}, 2, nil, 2, 2},
		{"non-multiple", []byte{1, 2, 3, 4, 5}, 2, nil, 3, 1},
		{"empty", nil, 4, nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks, err := Split(tc.data, tc.n)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err=%v want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || len(blocks) != tc.blocks {
				t.Fatalf("blocks=%d want %d err=%v", len(blocks), tc.blocks, err)
			}
			if tc.blocks > 0 && len(blocks[tc.blocks-1].Data) != tc.lastLen {
				t.Fatalf("last block len=%d want %d", len(blocks[tc.blocks-1].Data), tc.lastLen)
			}
		})
	}
}
