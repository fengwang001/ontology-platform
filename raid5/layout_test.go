package raid5

import "testing"

// TestLayoutMapping 覆盖 N=3,4,5 时每个条带的校验盘位置与数据盘排列，
// 并校验：校验盘 = N-1-(s mod N)；数据从校验盘下一盘起依次回绕。
func TestLayoutMapping(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		t.Run("N"+itoa(n), func(t *testing.T) {
			for s := 0; s < 2*n; s++ {
				p, data := stripeMap(n, s)
				wantP := (n - 1) - (s % n)
				t.Logf("N=%d stripe=%d 输入: s=%d -> 输出: parityDisk=%d dataDisks=%v 判定: 校验盘应=%d",
					n, s, s, p, data, wantP)
				if p != wantP {
					t.Fatalf("parity disk mismatch: got %d want %d", p, wantP)
				}
				if len(data) != n-1 {
					t.Fatalf("data block count = %d, want %d", len(data), n-1)
				}
				seen := map[int]bool{p: true}
				for k, d := range data {
					wantD := (p + 1 + k) % n
					if d != wantD {
						t.Fatalf("stripe %d data[%d]: got disk %d want %d", s, k, d, wantD)
					}
					if seen[d] {
						t.Fatalf("disk %d used twice in stripe %d", d, s)
					}
					seen[d] = true
				}
				if len(seen) != n {
					t.Fatalf("stripe %d does not use every disk exactly once: %v", s, seen)
				}
				// 逻辑块映射与条带映射一致。
				lb := s*(n-1) + 0
				ss, slot, disk := blockLocation(n, lb)
				if ss != s || slot != 0 || disk != data[0] {
					t.Fatalf("logical block map mismatch at lb=%d", lb)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 3 {
		return "3"
	}
	if n == 4 {
		return "4"
	}
	return "5"
}
