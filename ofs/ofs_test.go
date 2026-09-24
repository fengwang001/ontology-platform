package ofs

import "testing"

// 推进规则：表驱动，含乱序、重复、幂等。
func TestAckAdvance(t *testing.T) {
	cases := []struct {
		name string
		acks []int64
		want int64
	}{
		{"第三节序列", []int64{103, 100, 101, 105, 101, 102, 107}, 104},
		{"顺序", []int64{100, 101, 102, 103, 104, 105, 106, 107}, 108},
		{"逆序", []int64{107, 106, 105, 104, 103, 102, 101, 100}, 108},
		{"全重复", []int64{100, 100, 100}, 101},
		{"小于C幂等", []int64{100, 101, 100, 101}, 102},
		{"无Ack", nil, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(100)
			for off := int64(100); off <= 107; off++ {
				if err := s.Deliver(off); err != nil {
					t.Fatal(err)
				}
			}
			for _, off := range tc.acks {
				if err := s.Ack(off); err != nil {
					t.Fatal(err)
				}
			}
			if s.Committed() != tc.want {
				t.Fatalf("C=%d want %d", s.Committed(), tc.want)
			}
		})
	}
}

// 复杂度：推进只在 C 被 Ack 时发生，不做整表重扫。
// checked 是非导出字段，仅本包内（白盒）测试可观测，不进公开接口。
func TestAdvanceCheckCount(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		s := New(0)
		for off := int64(0); off <= m; off++ { // 已投递未提交 m+1 个
			if err := s.Deliver(off); err != nil {
				t.Fatal(err)
			}
		}
		for off := int64(1); off < m; off++ { // C 未 Ack，其后 m-1 个已 Ack
			if err := s.Ack(off); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Ack(m); err != nil { // Ack 最末（不在 C 上）：不得推进
			t.Fatal(err)
		}
		if s.checked > 2 {
			t.Fatalf("m=%d: Ack 不在 C 上却检查了 %d 个位点（随 m 增长）", m, s.checked)
		}
		if s.Committed() != 0 {
			t.Fatalf("m=%d: C 不应推进, got %d", m, s.Committed())
		}
		if err := s.Ack(0); err != nil { // Ack C：推进越过 0..m 共 m+1 个
			t.Fatal(err)
		}
		if s.Committed() != m+1 {
			t.Fatalf("m=%d: C=%d want %d", m, s.Committed(), m+1)
		}
		if int64(s.checked) > m+3 { // 不超过被推进越过的位点数加一个常数
			t.Fatalf("m=%d: checked=%d 超过推进位点数+常数", m, s.checked)
		}
	}
}

// 故障注入：Deliver 不连续、Ack 越界，报错且状态不变。
func TestRejectNoTrace(t *testing.T) {
	s := New(10)
	if err := s.Deliver(10); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(12); err != ErrGap {
		t.Fatalf("err=%v want ErrGap", err)
	}
	if err := s.Ack(11); err != ErrOutOfRange {
		t.Fatalf("err=%v want ErrOutOfRange", err)
	}
	if s.Committed() != 10 || s.Hi() != 11 || s.InFlight() != 1 {
		t.Fatalf("被拒操作改变了状态: C=%d hi=%d", s.Committed(), s.Hi())
	}
	if err := s.Ack(10); err != nil || s.Committed() != 11 { // 被拒后仍可用
		t.Fatalf("err=%v C=%d", err, s.Committed())
	}
}

// 崩溃重启：丢弃未提交状态，按已提交位点重建。
func TestReset(t *testing.T) {
	s := New(100)
	for off := int64(100); off <= 107; off++ {
		s.Deliver(off)
	}
	for _, off := range []int64{103, 100, 101, 105, 101, 102, 107} {
		s.Ack(off)
	}
	s.Reset()
	if s.Committed() != 104 || s.Hi() != 104 || s.InFlight() != 0 {
		t.Fatalf("Reset 后 C=%d hi=%d", s.Committed(), s.Hi())
	}
	for off := int64(104); off <= 107; off++ { // 重启后从 C 重新投递
		if err := s.Deliver(off); err != nil {
			t.Fatal(err)
		}
	}
}
