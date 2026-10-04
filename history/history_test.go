package history

import (
	"errors"
	"testing"
)

type fakeRetention struct {
	floor   int64
	removed []string
}

func (f *fakeRetention) PurgeExpiredLocked(now int64) []string {
	out := f.removed
	f.removed = nil
	return out
}

func (f *fakeRetention) RetainFloorLocked() int64 { return f.floor }

func TestIndexDeleteSeqAndNotFound(t *testing.T) {
	h := NewHistory()
	if seq, err := h.Index(0, []byte("a")); err != nil || seq != 1 {
		t.Fatalf("Index a = %d,%v", seq, err)
	}
	if seq, err := h.Index(0, []byte("b")); err != nil || seq != 2 {
		t.Fatalf("Index b = %d,%v", seq, err)
	}
	if seq, err := h.Delete(0, []byte("a")); err != nil || seq != 3 {
		t.Fatalf("Delete a = %d,%v", seq, err)
	}
	if _, err := h.Delete(0, []byte("a")); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("Delete missing doc err = %v, want ErrDocNotFound", err)
	}
	if _, err := h.Delete(0, []byte("zzz")); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("Delete never-seen err = %v", err)
	}
	// 被拒绝的 Delete 不占 seq。
	if seq, err := h.Index(0, []byte("c")); err != nil || seq != 4 {
		t.Fatalf("rejected op must not consume seq, got %d,%v", seq, err)
	}
	if h.MaxSeqLocked() != 4 {
		t.Fatalf("maxSeq=%d want 4", h.MaxSeqLocked())
	}
}

func TestRejectNoClockAdvance(t *testing.T) {
	h := NewHistory()
	if _, err := h.Index(5, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Index(4, []byte("b")); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back err=%v", err)
	}
	if _, err := h.Delete(4, []byte("missing")); !errors.Is(err, ErrClockBack) {
		// now=4 回退且文档不存在：文档不存在排在时钟回退之后，故报时钟回退。
		t.Fatalf("want ErrClockBack precedence, got %v", err)
	}
	if seq, err := h.Index(5, []byte("b")); err != nil || seq != 2 {
		t.Fatalf("after rejected ops seq=%d,%v, want 2", seq, err)
	}
}

func TestInvalidArgs(t *testing.T) {
	h := NewHistory()
	if _, err := h.Index(-1, []byte("a")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg now err=%v", err)
	}
	if _, err := h.Index(0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id err=%v", err)
	}
	big := make([]byte, 257)
	if _, err := h.Index(0, big); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("257B id err=%v", err)
	}
}

// TestMergeClearing 表驱动：Delete 与被取代者清除，存活 Index 保留。
func TestMergeClearing(t *testing.T) {
	build := func(ops []struct {
		kind Kind
		id   string
	}) *History {
		h := NewHistory()
		for _, op := range ops {
			var err error
			if op.kind == KindIndex {
				_, err = h.Index(0, []byte(op.id))
			} else {
				_, err = h.Delete(0, []byte(op.id))
			}
			if err != nil {
				t.Fatalf("setup %v %s: %v", op.kind, op.id, err)
			}
		}
		return h
	}

	cases := []struct {
		name string
		ops  []struct {
			kind Kind
			id   string
		}
		floor   int64
		cleared int
		wantNil []int64 // 期望被清成 nil 的 seq
		keep    []int64 // 期望保留的 seq
	}{
		{
			name: "superseded-index-and-delete-cleled-live-index-kept",
			ops: []struct {
				kind Kind
				id   string
			}{
				{KindIndex, "a"},  // 1 被 3 取代
				{KindIndex, "b"},  // 2 被 4 取代
				{KindDelete, "a"}, // 3 墓碑
				{KindIndex, "b"},  // 4 存活
				{KindIndex, "c"},  // 5 不在区间
			},
			floor:   5,
			cleared: 3,
			wantNil: []int64{1, 2, 3},
			keep:    []int64{4},
		},
		{
			name: "tombstone-cleared",
			ops: []struct {
				kind Kind
				id   string
			}{
				{KindIndex, "a"},
				{KindDelete, "a"},
			},
			floor:   3,
			cleared: 2,
			wantNil: []int64{1, 2},
		},
		{
			name: "live-index-below-floor-kept",
			ops: []struct {
				kind Kind
				id   string
			}{
				{KindIndex, "a"},
				{KindIndex, "b"},
			},
			floor:   3,
			cleared: 0,
			keep:    []int64{1, 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := build(tc.ops)
			ret := &fakeRetention{floor: tc.floor}
			res, err := h.Merge(0, ret)
			if err != nil {
				t.Fatal(err)
			}
			if res.Cleared != tc.cleared {
				t.Fatalf("cleared=%d want %d", res.Cleared, tc.cleared)
			}
			if h.HLocked() != tc.floor {
				t.Fatalf("H=%d want %d", h.HLocked(), tc.floor)
			}
			for _, seq := range tc.wantNil {
				if h.ops[seq-1] != nil {
					t.Fatalf("seq %d should be cleared", seq)
				}
			}
			for _, seq := range tc.keep {
				if h.ops[seq-1] == nil {
					t.Fatalf("seq %d should be kept", seq)
				}
			}
		})
	}
}

// TestTouchedBound 证明单次 Merge 触碰量 == floor-旧H，与历史总条数无关。
func TestTouchedBound(t *testing.T) {
	measure := func(total int) int {
		h := NewHistory()
		for i := 0; i < total; i++ {
			// 全部存活 Index（不复用 id），清除条数会是 0，但触碰量只取决于区间大小。
			if _, err := h.Index(0, []byte("doc-"+itoa(i)+"-pad")); err != nil {
				t.Fatal(err)
			}
		}
		oldH := h.HLocked()
		ret := &fakeRetention{floor: oldH + 10}
		if _, err := h.Merge(0, ret); err != nil {
			t.Fatal(err)
		}
		return h.TouchedLocked()
	}
	n1 := measure(1000)
	n2 := measure(100000)
	if n1 != 10 || n2 != 10 {
		t.Fatalf("touched: 1000hist=%d 100000hist=%d, both must be 10", n1, n2)
	}
	t.Logf("touched bound verified: 1000 条=%d, 100000 条=%d（区间同为 10）", n1, n2)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
