package dedup

import "testing"

// TestMembershipCheckIsConstant 证明成员判定是 O(1)：
// 先喂 m 个不同 Seq，再查一个全新 Seq，检查条目数不随 m 增长。
func TestMembershipCheckIsConstant(t *testing.T) {
	const maxChecked = 2 // 与 m 无关的小常数上界
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if !s.Add(int64(i + 1)) {
				t.Fatalf("m=%d: 第 %d 个新 Seq 应插入成功", m, i+1)
			}
		}
		if s.Contains(int64(m + 1)) {
			t.Fatalf("m=%d: 全新 Seq 不应存在", m)
		}
		if s.lastChecked > maxChecked {
			t.Fatalf("m=%d: 检查了 %d 条，超过常数上界 %d（疑似线性扫描）", m, s.lastChecked, maxChecked)
		}
	}
}

// TestAddIdempotent 表驱动：重复 Add 是 no-op。
func TestAddIdempotent(t *testing.T) {
	cases := []struct {
		name string
		seqs []int64
		want []bool
		len  int
	}{
		{"全新", []int64{1, 2, 3}, []bool{true, true, true}, 3},
		{"重复", []int64{7, 7, 7}, []bool{true, false, false}, 1},
		{"交错", []int64{1, 2, 1, 3, 2}, []bool{true, true, false, true, false}, 3},
	}
	for _, c := range cases {
		s := New()
		for i, seq := range c.seqs {
			if got := s.Add(seq); got != c.want[i] {
				t.Errorf("%s: Add(%d) 第 %d 次 = %v，want %v", c.name, seq, i+1, got, c.want[i])
			}
		}
		if s.Len() != c.len {
			t.Errorf("%s: Len() = %d，want %d", c.name, s.Len(), c.len)
		}
	}
}
