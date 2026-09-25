package unwrap

import (
	"errors"
	"testing"
)

// TestChecked 证明展开只与 last 比较（每步 O(1)）：对 m 个严格递增序号
// （含多次回绕），每次 Feed 检查的历史序号数 ≤1，总检查数恰好等于 m。
// 若实现退化为扫描全部历史找归属，总检查数会到 m²/2 量级。
func TestChecked(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		u, err := New(8) // M=256，m 远大于 M，强制多次回绕
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for i := 0; i < m; i++ {
			if _, err := u.Feed(uint64(i) & 255); err != nil {
				t.Fatalf("m=%d step %d: %v", m, i, err)
			}
			if u.checked > 1 {
				t.Fatalf("m=%d step %d: checked=%d > 1", m, i, u.checked)
			}
			total += u.checked
		}
		if total != m {
			t.Fatalf("m=%d: total checked=%d, want exactly %d", m, total, m)
		}
		if u.Last() != int64(m-1) {
			t.Fatalf("m=%d: last=%d, want %d", m, u.Last(), m-1)
		}
	}
}

// TestFeedMonotonic 不变量 3：被接受的绝对序号严格递增，Last 单调不减。
func TestFeedMonotonic(t *testing.T) {
	for _, n := range []int{4, 16, 63} {
		u, _ := New(n)
		mask := uint64(1)<<n - 1
		prev := int64(-1)
		for i := 0; i < 5000; i++ {
			abs, err := u.Feed(uint64(i) & mask)
			if err != nil {
				t.Fatalf("N=%d i=%d: %v", n, i, err)
			}
			if abs <= prev {
				t.Fatalf("N=%d i=%d: abs=%d not > prev=%d", n, i, abs, prev)
			}
			prev = abs
		}
		if u.Last() != prev { // Last 与最后返回值一致且单调不减
			t.Fatalf("N=%d: Last=%d, want %d", n, u.Last(), prev)
		}
		again, err := u.Feed(uint64(4999) & mask) // Equal 幂等：不前进、不报错
		if err != nil || again != prev || u.Last() != prev {
			t.Fatalf("N=%d: idempotent repeat got (%d,%v)", n, again, err)
		}
	}
}

// TestFeedErrors 不变量 4：四类可判定错误互不相同，被拒后 last 不变且可继续用。
func TestFeedErrors(t *testing.T) {
	for _, n := range []int{-1, 0, 64, 100} { // 宽度非法
		if _, err := New(n); !errors.Is(err, ErrWidth) {
			t.Fatalf("New(%d): err=%v, want ErrWidth", n, err)
		}
	}
	u, _ := New(4)
	u.Feed(13)
	if _, err := u.Feed(16); !errors.Is(err, ErrOutOfRange) { // s >= M
		t.Fatalf("out-of-range: %v", err)
	}
	if u.Last() != 13 {
		t.Fatalf("last moved on rejection: %d", u.Last())
	}
	u.Feed(14)
	u.Feed(15)
	if _, err := u.Feed(7); !errors.Is(err, ErrIncomparable) { // d=8 半圈
		t.Fatalf("half-circle: %v", err)
	}
	if _, err := u.Feed(14); !errors.Is(err, ErrMovedBackward) { // d=15 倒退
		t.Fatalf("backward: %v", err)
	}
	if u.Last() != 15 {
		t.Fatalf("last moved on rejection: %d", u.Last())
	}
	if abs, err := u.Feed(0); err != nil || abs != 16 { // 被拒后仍可正常使用
		t.Fatalf("feed after rejection: (%d,%v)", abs, err)
	}
	errs := []error{ErrWidth, ErrOutOfRange, ErrIncomparable, ErrMovedBackward}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatalf("sentinels %d,%d identical", i, j)
			}
		}
	}
}
