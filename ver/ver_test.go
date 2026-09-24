package ver

import (
	"math/bits"
	"testing"
)

// 二分定位证明：任意 TS 的检查个数不超过 ⌈log2(m+1)⌉+3。
// 白箱测试：直接读非导出字段 checked（不经过任何导出函数）。
func TestAsOfCheckCountLogBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := &Store{}
		for i := 0; i < m; i++ {
			s.Upsert(int64(i), "v")
		}
		bound := bits.Len(uint(m)) + 3 // bits.Len(m) == ⌈log2(m+1)⌉
		tss := []int64{-1, 0, 1, int64(m / 3), int64(m / 2), int64(m - 1), int64(m), int64(2 * m)}
		for _, ts := range tss {
			s.AsOf(ts)
			if s.checked > bound {
				t.Errorf("m=%d ts=%d: checked %d > bound %d", m, ts, s.checked, bound)
			}
		}
	}
}

// 同 ValidFrom 覆盖、墓碑、左闭右开边界、首版本之前与末版本之后。
func TestStoreAsOf(t *testing.T) {
	s := &Store{}
	s.Upsert(10, "A")
	s.Upsert(20, "B")
	s.Upsert(20, "B2") // 覆盖同 ValidFrom
	s.Delete(30)       // 墓碑
	s.Delete(30)       // 墓碑覆盖墓碑
	s.Upsert(40, "D")
	s.Delete(50)
	s.Upsert(50, "E") // 版本覆盖墓碑
	cases := []struct {
		ts    int64
		val   string
		found bool
	}{
		{9, "", false},    // 第一个版本之前
		{10, "A", true},   // 恰好等于 ValidFrom（左闭）
		{19, "A", true},   // 区间内部
		{20, "B2", true},  // 覆盖生效
		{29, "B2", true},  // 下一版本之前（右开）
		{30, "", false},   // 墓碑区间起点
		{39, "", false},   // 墓碑区间内部
		{40, "D", true},   // 墓碑之后
		{49, "D", true},   //
		{50, "E", true},   // 墓碑被版本覆盖
		{1000, "E", true}, // 末版本之后（正无穷）
	}
	for _, c := range cases {
		if val, found := s.AsOf(c.ts); val != c.val || found != c.found {
			t.Errorf("ts=%d: got (%q,%v), want (%q,%v)", c.ts, val, found, c.val, c.found)
		}
	}
}
