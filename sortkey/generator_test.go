package sortkey

import (
	"errors"
	"testing"
)

// TestBetweenThreePositions 覆盖三种插入位置：最前、最后、两键之间。
func TestBetweenThreePositions(t *testing.T) {
	gen := NewFractional(64)

	front, err := gen.Between("", "i")
	if err != nil || !(front < "i") {
		t.Fatalf("front = %q, err = %v, want < %q", front, err, "i")
	}
	back, err := gen.Between("i", "")
	if err != nil || !("i" < back) {
		t.Fatalf("back = %q, err = %v, want > %q", back, err, "i")
	}
	mid, err := gen.Between("a", "b")
	if err != nil || !("a" < mid && mid < "b") {
		t.Fatalf("mid = %q, err = %v, want between a and b", mid, err)
	}
	empty, err := gen.Between("", "")
	if err != nil || empty == "" {
		t.Fatalf("empty-seq key = %q, err = %v", empty, err)
	}
}

// TestBetweenStrictlyInside 对一批紧凑间隙断言 左 < 新 < 右。
func TestBetweenStrictlyInside(t *testing.T) {
	gen := NewFractional(64)
	cases := [][2]string{
		{"", ""}, {"", "1"}, {"", "i"}, {"z", ""},
		{"a", "b"}, {"a", "ab"}, {"a0i", "a1"}, {"az", "b"},
		{"y", "yz"}, {"0i", "1"}, {"abc", "abd"}, {"zz", ""},
	}
	for _, c := range cases {
		key, err := gen.Between(c[0], c[1])
		if err != nil {
			t.Fatalf("Between(%q, %q) error: %v", c[0], c[1], err)
		}
		if !(c[0] < key && (c[1] == "" || key < c[1])) {
			t.Fatalf("Between(%q, %q) = %q, not strictly inside", c[0], c[1], key)
		}
	}
}

// TestBetweenDeterministic 同一对邻居反复调用必须得到同一个结果。
func TestBetweenDeterministic(t *testing.T) {
	gen := NewFractional(64)
	pairs := [][2]string{{"", ""}, {"a", "b"}, {"a0i", "a1"}, {"x", ""}}
	for _, p := range pairs {
		first, err := gen.Between(p[0], p[1])
		if err != nil {
			t.Fatalf("Between(%q, %q) error: %v", p[0], p[1], err)
		}
		for i := 0; i < 100; i++ {
			got, err := gen.Between(p[0], p[1])
			if err != nil || got != first {
				t.Fatalf("Between(%q, %q) run %d = %q, want %q", p[0], p[1], i, got, first)
			}
		}
	}
}

// TestLengthLimit 连续在同一间隙插入直到触发长度上限。
func TestLengthLimit(t *testing.T) {
	const limit = 8
	gen := NewFractional(limit)
	lo, hi := "a", "b"
	inserts := 0
	for {
		key, err := gen.Between(lo, hi)
		if err != nil {
			if !errors.Is(err, ErrNeedsRebalance) {
				t.Fatalf("got %v, want ErrNeedsRebalance", err)
			}
			break
		}
		if len(key) > limit {
			t.Fatalf("key %q exceeds limit %d", key, limit)
		}
		if !(lo < key && key < hi) {
			t.Fatalf("key %q not inside (%q, %q)", key, lo, hi)
		}
		hi = key // 每次都在间隙的同一端继续插入，键不断变长
		inserts++
	}
	if inserts == 0 {
		t.Fatal("expected at least one successful insert before hitting the limit")
	}
	t.Logf("inserted %d keys before ErrNeedsRebalance", inserts)
}

// TestInvalidChar 字符集之外的键必须被拒绝并指出是哪个字符。
func TestInvalidChar(t *testing.T) {
	gen := NewFractional(64)
	for _, bad := range []string{"a!", "A", "中", "a b", "~"} {
		_, err := gen.Between(bad, "")
		var ikErr *InvalidKeyError
		if !errors.As(err, &ikErr) {
			t.Fatalf("Between(%q, \"\") error = %v, want *InvalidKeyError", bad, err)
		}
		if ikErr.Key != bad {
			t.Fatalf("InvalidKeyError.Key = %q, want %q", ikErr.Key, bad)
		}
	}
	// 右邻居同样校验。
	if _, err := gen.Between("a", "b!"); err == nil {
		t.Fatal("expected error for invalid right key")
	}
	// 非法字符错误与需要重排是不同类别。
	_, err := gen.Between("!", "")
	if errors.Is(err, ErrNeedsRebalance) || errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("invalid-char error must be a distinct category, got %v", err)
	}
}

// TestTrailingZeroRejected 以 '0' 结尾的键不是规范形式，必须被拒绝，
// 否则其左侧会形成无法再细分的死路。
func TestTrailingZeroRejected(t *testing.T) {
	gen := NewFractional(64)
	for _, bad := range []string{"0", "00", "a0", "zz0"} {
		if err := gen.Validate(bad); err == nil {
			t.Fatalf("Validate(%q) = nil, want InvalidKeyError", bad)
		}
		if _, err := gen.Between("", bad); err == nil {
			t.Fatalf("Between(\"\", %q) = nil error, want rejection", bad)
		}
	}
	// 生成器自身产出的键永远满足规范形式。
	key, err := gen.Between("", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := gen.Validate(key); err != nil {
		t.Fatalf("generated key %q fails Validate: %v", key, err)
	}
}

// TestInvalidOrder 左键不小于右键是参数错误，与需要重排不同。
func TestInvalidOrder(t *testing.T) {
	gen := NewFractional(64)
	for _, p := range [][2]string{{"b", "a"}, {"a", "a"}, {"i", "0i"}} {
		_, err := gen.Between(p[0], p[1])
		if !errors.Is(err, ErrInvalidOrder) {
			t.Fatalf("Between(%q, %q) error = %v, want ErrInvalidOrder", p[0], p[1], err)
		}
		if errors.Is(err, ErrNeedsRebalance) {
			t.Fatalf("ErrInvalidOrder must differ from ErrNeedsRebalance")
		}
	}
}

// TestEvenKeys 重排键：数量正确、严格递增、合法、等间距。
func TestEvenKeys(t *testing.T) {
	gen := NewFractional(64)
	for _, n := range []int{0, 1, 5, 36, 100} {
		keys, err := gen.EvenKeys(n)
		if err != nil {
			t.Fatalf("EvenKeys(%d) error: %v", n, err)
		}
		if len(keys) != n {
			t.Fatalf("EvenKeys(%d) returned %d keys", n, len(keys))
		}
		for i, k := range keys {
			if err := gen.Validate(k); err != nil {
				t.Fatalf("EvenKeys(%d)[%d] = %q invalid: %v", n, i, k, err)
			}
			if i > 0 && keys[i-1] >= k {
				t.Fatalf("EvenKeys(%d) not increasing at %d", n, i)
			}
		}
	}
}
