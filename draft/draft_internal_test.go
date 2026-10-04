package draft

import "testing"

// TestAutoPickTouched 从包内观测：一次超时自动选人（预选检查失败 + 最小可用查找）
// 读取的英雄池记录数不超过 40，且 H=64 与 H=65536 两档几乎相同。
func TestAutoPickTouched(t *testing.T) {
	var counts []int
	for _, h := range []int{64, 65536} {
		d, err := New(2, 0, 2, 0, 1000, 0, h, 0)
		if err != nil {
			t.Fatal(err)
		}
		// 手动选/禁掉若干小号英雄。
		for _, hero := range []int{1, 2, 3, 5, 8, 13, 21, 34, 63, 64, 65, 100, 40000} {
			if hero <= h {
				d.pool.Remove(hero)
			}
		}
		// 玩家 0 预选一个已不可用的英雄，强制走最小可用查找（应得 4）。
		d.hover[0] = 5
		d.pool.ResetTouched()
		d.autoPick(1)
		n := d.pool.Touched()
		counts = append(counts, n)
		t.Logf("H=%d autoPick touched=%d picked=%d", h, n, d.picks[0])
		if d.picks[0] != 4 {
			t.Fatalf("H=%d auto pick = %d want 4", h, d.picks[0])
		}
		if n > 40 {
			t.Fatalf("H=%d touched=%d > 40", h, n)
		}
	}
	diff := counts[1] - counts[0]
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Fatalf("touched differs by %d across 1024x H: %v", diff, counts)
	}
}
