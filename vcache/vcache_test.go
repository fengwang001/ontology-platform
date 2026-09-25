package vcache

import (
	"fmt"
	"testing"

	"ontology/ver"
)

// TestVersionMonotonic 钉住不变量 3：任何 Write 不让任何 stamp 回退。
func TestVersionMonotonic(t *testing.T) {
	st := ver.New()
	prevG, prevT := map[string]int64{}, st.TotalStamp()
	for i := 0; i < 300; i++ {
		g := fmt.Sprintf("g%d", i%4)
		if err := st.Write(g, fmt.Sprintf("k%d", i), int64(i)); err != nil {
			t.Fatal(err)
		}
		for _, gg := range st.Groups() {
			if st.Stamp(gg) < prevG[gg] {
				t.Fatalf("stamp(%s) regressed", gg)
			}
			prevG[gg] = st.Stamp(gg)
		}
		if st.TotalStamp() < prevT {
			t.Fatal("totalStamp regressed")
		}
		prevT = st.TotalStamp()
	}
}

// TestCacheFreshAfterRead 钉住不变量 2：每次 ReadG/ReadTotal 返回后，
// 对应缓存的 stamp 恰等于当前 stamp（绝不返回过期值）。
func TestCacheFreshAfterRead(t *testing.T) {
	st := ver.New()
	c := New(st)
	writes := []struct {
		g, k string
		v    int64
	}{
		{"g0", "a", 5}, {"g0", "b", 3}, {"g1", "c", 7}, {"g0", "b", 10},
	}
	for i, w := range writes {
		if err := st.Write(w.g, w.k, w.v); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if _, err := c.ReadG(w.g); err != nil {
			t.Fatalf("readG %d: %v", i, err)
		}
		if got, want := c.cg[w.g].stamp, st.Stamp(w.g); got != want {
			t.Fatalf("step %d: cacheG[%q].stamp=%d, want %d", i, w.g, got, want)
		}
		if got := c.ReadTotal(); got != st.SumTotal() {
			t.Fatalf("step %d: ReadTotal=%d, want %d", i, got, st.SumTotal())
		}
		if got, want := c.ct.stamp, st.TotalStamp(); got != want {
			t.Fatalf("step %d: cacheT.stamp=%d, want %d", i, got, want)
		}
	}
}

// TestFreshnessProbeConstant 钉住第四节复杂度约束：先写 m 条记录使缓存
// 有效，再写一条令其失效，再读触发重算；「判新鲜」遍历的记录数（probe）
// 必须是不随 m 增长的小常数（版本戳增量维护，判新鲜 O(1)）。
func TestFreshnessProbeConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st := ver.New()
		c := New(st)
		for i := 0; i < m; i++ {
			if err := st.Write("g0", fmt.Sprintf("k%d", i), int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.ReadG("g0"); err != nil { // 使缓存有效
			t.Fatal(err)
		}
		if err := st.Write("g0", "k0", 1); err != nil { // 令其失效
			t.Fatal(err)
		}
		got, err := c.ReadG("g0") // 触发失效重算
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(m*(m-1)/2) + 1; got != want {
			t.Fatalf("m=%d: ReadG=%d, want %d", m, got, want)
		}
		const smallConst = 4 // 与 m 无关的上界；实际恒为 0
		if c.probe > smallConst {
			t.Fatalf("m=%d: freshness check traversed %d records, want <= %d", m, c.probe, smallConst)
		}
	}
}
