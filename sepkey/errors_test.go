package sepkey

import (
	"bytes"
	"errors"
	"testing"
)

// 校验 AddBlock 的错误优先级: 空键 → last>=next → last<上一块next → 已Finish,
// 且被拒绝的操作不改变已登记内容。
func TestAddBlockErrorOrder(t *testing.T) {
	var b Builder
	mustAdd(t, &b, []byte("m"), []byte("p"))
	mustFinish(t, &b, []byte("z"))
	before := b.Seps()

	cases := []struct {
		name    string
		last    []byte
		next    []byte
		wantErr error
	}{
		{"空last优先于乱序", nil, []byte("a"), ErrEmptyKey},
		{"空next优先于乱序", []byte("a"), nil, ErrEmptyKey},
		{"last等于next", []byte("a"), []byte("a"), ErrOutOfOrder},
		{"last大于next", []byte("b"), []byte("a"), ErrOutOfOrder},
		{"乱序优先于非单调", []byte("a"), []byte("a"), ErrOutOfOrder},
		{"last小于上一块next", []byte("o"), []byte("q"), ErrNotMonotonic},
		{"非单调优先于已Finish", []byte("o"), []byte("q"), ErrNotMonotonic},
		{"已Finish", []byte("p"), []byte("q"), ErrFinished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.AddBlock(tc.last, tc.next)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, 期望 %v", err, tc.wantErr)
			}
			after := b.Seps()
			if len(after) != len(before) {
				t.Fatalf("被拒绝的 AddBlock 改变了已登记内容: %q -> %q", before, after)
			}
			for i := range before {
				if !bytes.Equal(before[i], after[i]) {
					t.Fatalf("被拒绝的 AddBlock 改变了已登记内容: %q -> %q", before, after)
				}
			}
		})
	}
}

// 校验 Finish 的错误优先级: 空键 → 小于上一块next → 已Finish。
func TestFinishErrorOrder(t *testing.T) {
	var b Builder
	mustAdd(t, &b, []byte("m"), []byte("p"))
	mustFinish(t, &b, []byte("z"))
	before := b.Seps()

	cases := []struct {
		name    string
		last    []byte
		wantErr error
	}{
		{"空键", nil, ErrEmptyKey},
		{"小于上一块next", []byte("o"), ErrNotMonotonic},
		{"已Finish", []byte("zz"), ErrFinished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Finish(tc.last)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, 期望 %v", err, tc.wantErr)
			}
			after := b.Seps()
			if len(after) != len(before) {
				t.Fatalf("被拒绝的 Finish 改变了已登记内容: %q -> %q", before, after)
			}
		})
	}
}

// 上一块 next 等于本块 last 是合法的（next 须不大于本块 last）。
func TestAddBlockPrevNextEqualLast(t *testing.T) {
	var b Builder
	mustAdd(t, &b, []byte("a"), []byte("c"))
	mustAdd(t, &b, []byte("c"), []byte("e"))
	mustFinish(t, &b, []byte("f"))
	if got := len(b.Seps()); got != 3 {
		t.Fatalf("块数=%d, 期望 3", got)
	}
}

// Finish 之前调用 Seek 必须被拒绝。
func TestSeekBeforeFinish(t *testing.T) {
	var b Builder
	if _, err := b.Seek([]byte("a")); !errors.Is(err, ErrNotFinished) {
		t.Fatalf("空构建器 Seek: err=%v, 期望 ErrNotFinished", err)
	}
	mustAdd(t, &b, []byte("a"), []byte("c"))
	if _, err := b.Seek([]byte("a")); !errors.Is(err, ErrNotFinished) {
		t.Fatalf("未 Finish 时 Seek: err=%v, 期望 ErrNotFinished", err)
	}
}
