package shardmap

import (
	"errors"
	"fmt"
	"testing"

	"ontology/slot"
)

// reset 仅供本包测试使用：清空全局表与重分布器。
func reset() {
	mu.Lock()
	indexes = map[string]*entry{}
	resharder = nil
	mu.Unlock()
}

func hfunc(v uint32) slot.HashFunc {
	return func([]byte) uint32 { return v }
}

func TestCreateValidation(t *testing.T) {
	cases := []struct {
		name    string
		n, r, p int
		h       slot.HashFunc
		want    error
	}{
		{"ok", 4, 8, 1, nil, nil},
		{"ok-p", 4, 8, 3, nil, nil},
		{"n-zero", 0, 8, 1, nil, ErrInvalidArgument},
		{"n-big", 1025, 2048, 1, nil, ErrInvalidArgument},
		{"r-lt-n", 4, 3, 1, nil, ErrInvalidArgument},
		{"r-big", 4, 1<<20 + 1, 1, nil, ErrInvalidArgument},
		{"r-not-div", 4, 9, 1, nil, ErrInvalidArgument},
		{"p-bad-1", 4, 8, 0, nil, ErrInvalidArgument},
		{"p-eq-n", 4, 8, 4, nil, ErrInvalidArgument},
		{"p-gt-n", 4, 8, 5, nil, ErrInvalidArgument},
		{"n1-p1", 1, 4, 1, nil, nil},
		{"n1-p2", 1, 4, 2, nil, ErrInvalidArgument},
		{"two-hashers", 4, 8, 1, nil, ErrInvalidArgument}, // len>1 通过独立调用覆盖
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reset()
			name := fmt.Sprintf("idx%d", i)
			var err error
			if c.name == "two-hashers" {
				err = CreateIndex(name, c.n, c.r, c.p, hfunc(1), hfunc(2))
			} else {
				err = CreateIndex(name, c.n, c.r, c.p, c.h)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want=%v", err, c.want)
			}
			if err == nil {
				err = CreateIndex(name, c.n, c.r, c.p, c.h)
				if !errors.Is(err, ErrExists) {
					t.Fatalf("duplicate err=%v want ErrExists", err)
				}
			}
		})
	}
}

func TestSnapshotAndNotFound(t *testing.T) {
	reset()
	if _, err := Snapshot("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if err := SetWriteBlock("nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestSplitValidationThreeRules(t *testing.T) {
	// 题面：N=2,R=8
	cases := []struct {
		name    string
		n2      int
		blocked bool
		want    error
	}{
		{"n2-to-big", 1025, true, ErrInvalidArgument},
		{"not-blocked", 4, false, ErrNotWriteBlocked},
		{"eq-n", 2, true, ErrNotSplittable},         // 不大于 N
		{"lt-n", 1, true, ErrNotSplittable},         // 2 不整除 1 且 1<2
		{"not-multiple", 3, true, ErrNotSplittable}, // 2 不整除 3
		{"r-not-div", 16, true, ErrNotSplittable},   // 16 不整除 8
		{"ok", 4, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reset()
			if err := CreateIndex("i", 2, 8, 1); err != nil {
				t.Fatal(err)
			}
			if c.blocked {
				if err := SetWriteBlock("i", true); err != nil {
					t.Fatal(err)
				}
			}
			err := Split("i", c.n2)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want=%v", err, c.want)
			}
			if c.want != nil {
				st, _ := Snapshot("i")
				if st.N != 2 {
					t.Fatalf("state changed after rejection: N=%d", st.N)
				}
			}
		})
	}
	// 成功路径 N: 2 -> 4 -> 8
	reset()
	if err := CreateIndex("i", 2, 8, 1); err != nil {
		t.Fatal(err)
	}
	if err := SetWriteBlock("i", true); err != nil {
		t.Fatal(err)
	}
	if err := Split("i", 4); err != nil {
		t.Fatal(err)
	}
	if err := Split("i", 8); err != nil {
		t.Fatal(err)
	}
	if st, _ := Snapshot("i"); st.N != 8 {
		t.Fatalf("N=%d want 8", st.N)
	}
}

func TestShrinkValidation(t *testing.T) {
	cases := []struct {
		name    string
		n, r, p int
		n2      int
		blocked bool
		want    error
	}{
		{"not-blocked", 4, 8, 1, 2, false, ErrNotWriteBlocked},
		{"n2-eq-n", 4, 8, 1, 4, true, ErrNotShrinkable},
		{"n2-gt-n", 4, 8, 1, 8, true, ErrNotShrinkable},
		{"not-divisor", 4, 8, 1, 3, true, ErrNotShrinkable},
		{"p-blocks", 4, 8, 3, 2, true, ErrNotShrinkable}, // P=3, N2=2 => N2<=P
		{"p-ok", 8, 16, 3, 4, true, nil},                 // N2=4 > P=3
		{"ok", 4, 8, 1, 2, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reset()
			if err := CreateIndex("i", c.n, c.r, c.p); err != nil {
				t.Fatal(err)
			}
			if c.blocked {
				if err := SetWriteBlock("i", true); err != nil {
					t.Fatal(err)
				}
			}
			err := Shrink("i", c.n2)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want=%v", err, c.want)
			}
		})
	}
}

func TestSetWriteBlockBothDirections(t *testing.T) {
	reset()
	if err := CreateIndex("i", 4, 8, 1); err != nil {
		t.Fatal(err)
	}
	if st, _ := Snapshot("i"); st.Block {
		t.Fatal("should start unblocked")
	}
	if err := SetWriteBlock("i", true); err != nil {
		t.Fatal(err)
	}
	if st, _ := Snapshot("i"); !st.Block {
		t.Fatal("should be blocked")
	}
	if err := SetWriteBlock("i", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := Snapshot("i"); st.Block {
		t.Fatal("should be unblocked again")
	}
}

func TestSearchShardsRouting(t *testing.T) {
	reset()
	if err := CreateIndex("i", 4, 8, 3, hfunc(6)); err != nil {
		t.Fatal(err)
	}
	got, err := SearchShards("i", []byte("rt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("got=%v want [0 3]", got)
	}
	if _, err := SearchShards("i", nil); !errors.Is(err, ErrMissingRouting) {
		t.Fatalf("err=%v", err)
	}
	if _, err := SearchShards("missing", []byte("rt")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	// 参数非法优先于索引不存在
	reset()
	if err := Split("ghost", 99999); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err=%v want invalid argument", err)
	}
	if err := Shrink("ghost", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err=%v", err)
	}
}
