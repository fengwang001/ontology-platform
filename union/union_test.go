package union

import "testing"

// TestScanCountBounded 钉住第四节复杂度约束：Add/Withdraw 为定位接触/
// 重叠区间而线性检查的区间个数（非导出计数器 scanned）不随总段数 m
// 增长——二分定位 + 局部前向扫描，而不是整表扫描。
func TestScanCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		u := New(m + 1)
		for i := 0; i < m; i++ {
			if _, err := u.Add(int64(10*i), int64(10*i+5)); err != nil {
				t.Fatalf("m=%d add %d: %v", m, i, err)
			}
		}
		k := int64(m / 2)
		// 只与第 k 段严格重叠的撤回：定位扫描应是个小常数
		//（1 段命中 + 1 次终止检查），与 m 无关。
		if _, err := u.Withdraw(10*k+2, 10*k+3); err != nil {
			t.Fatalf("m=%d withdraw: %v", m, err)
		}
		if u.scanned > 4 {
			t.Errorf("m=%d: withdraw scanned=%d, want <= 4（不随 m 增长）", m, u.scanned)
		}
		// 只与第 k 段邻接的并入同理。
		if _, err := u.Add(10*k+5, 10*k+6); err != nil {
			t.Fatalf("m=%d add: %v", m, err)
		}
		if u.scanned > 4 {
			t.Errorf("m=%d: add scanned=%d, want <= 4（不随 m 增长）", m, u.scanned)
		}
	}
}
