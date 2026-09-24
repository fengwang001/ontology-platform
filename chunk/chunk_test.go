package chunk

import (
	"math/rand"
	"testing"
)

// 滚动正确性 + 代价上界：对每个偏移，滚动结果必须与从头重算相等，
// 且基本运算总次数不超过 4*数据长度。
func TestRollMatchesRecompute(t *testing.T) {
	cases := []struct{ n, size int }{
		{1000, 4}, {1024, 16}, {999, 7}, {64, 64}, {65, 64}, {3, 100},
	}
	for _, c := range cases {
		rng := rand.New(rand.NewSource(int64(c.n*131 + c.size)))
		data := make([]byte, c.n)
		rng.Read(data)
		if c.n < c.size { // 数据不足一个窗口，无滚动可言
			continue
		}
		// 第一遍：纯滚动，统计基本运算次数。
		ResetCounters()
		rolled := make([]uint32, 0, c.n-c.size+1)
		w := WeakSum(data[:c.size])
		rolled = append(rolled, w)
		for i := 0; i+c.size < c.n; i++ {
			w = Roll(w, data[i], data[i+c.size])
			rolled = append(rolled, w)
		}
		if ops := WeakOps(); ops > int64(4*c.n) {
			t.Fatalf("n=%d size=%d：弱校验运算 %d 次，超过 4n=%d", c.n, c.size, ops, 4*c.n)
		}
		// 第二遍：每个偏移从头重算，逐个与滚动结果比对。
		for i := 0; i+c.size <= c.n; i++ {
			if fresh := WeakSum(data[i : i+c.size]); rolled[i] != fresh {
				t.Fatalf("n=%d size=%d 偏移 %d：滚动 %d != 重算 %d", c.n, c.size, i, rolled[i], fresh)
			}
		}
	}
}

// 弱校验和碰撞可构造：内容不同但弱校验和相同，强校验和必须不同。
func TestWeakCollisionPairs(t *testing.T) {
	cases := []struct{ a, b []byte }{
		{[]byte{1, 2, 3, 4}, []byte{4, 3, 2, 1}},
		{[]byte{0, 0, 0, 10}, []byte{1, 2, 3, 4}},
		{[]byte("ab"), []byte("ba")},
	}
	for _, c := range cases {
		if WeakSum(c.a) != WeakSum(c.b) {
			t.Fatalf("%v 与 %v 应当弱碰撞", c.a, c.b)
		}
		if string(c.a) == string(c.b) {
			t.Fatalf("%v 与 %v 内容应当不同", c.a, c.b)
		}
		if StrongSum(c.a) == StrongSum(c.b) {
			t.Fatalf("%v 与 %v 强校验和不应相同", c.a, c.b)
		}
	}
}

// 切分边界：整数倍、非整数倍、末块不满、块大于数据、非法块大小。
func TestSplit(t *testing.T) {
	cases := []struct {
		n, size         int
		blocks, lastLen int
	}{
		{64, 16, 4, 16},
		{65, 16, 5, 1},
		{16, 16, 1, 16},
		{3, 100, 1, 3},
		{0, 16, 0, 0},
		{10, 0, 0, 0},
	}
	for _, c := range cases {
		if got := NumBlocks(c.n, c.size); got != c.blocks {
			t.Fatalf("NumBlocks(%d,%d)=%d，期望 %d", c.n, c.size, got, c.blocks)
		}
		if c.blocks > 0 {
			if got := BlockLen(c.n, c.size, c.blocks-1); got != c.lastLen {
				t.Fatalf("末块长度=%d，期望 %d", got, c.lastLen)
			}
		}
	}
}
