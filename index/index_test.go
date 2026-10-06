package index

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/domain"
)

func TestUpsertRemoveRange(t *testing.T) {
	b := &Bucket{}
	entries := []Entry{{10, "a"}, {5, "b"}, {10, "c"}, {1, "z"}, {20, "m"}}
	for _, e := range entries {
		b.Upsert(e)
	}
	got, _, _ := b.Range(0, 10)
	want := []Entry{{1, "z"}, {5, "b"}, {10, "a"}, {10, "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Range got %v want %v", got, want)
	}
	b.Remove(Entry{10, "a"})
	got, _, _ = b.Range(0, 10)
	want = []Entry{{1, "z"}, {5, "b"}, {10, "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after remove got %v want %v", got, want)
	}
	// 区间下界排除已超期记录
	got, _, _ = b.Range(6, 20)
	want = []Entry{{10, "c"}, {20, "m"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("range [6,20] got %v want %v", got, want)
	}
}

// TestRangeComplexity 两档对象总数对照：命中条数固定时，
// 查询的比较次数与访问元素数不得随总数线性增长。
func TestRangeComplexity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sizes := []int{10_000, 100_000}
	var prevCmps, prevVisited int
	for _, n := range sizes {
		ix := New()
		// 随机撒入 n 条记录，到期日散布在 [0, 10*n)
		for i := 0; i < n; i++ {
			ix.Upsert(domain.CatBoiler, Entry{Expiry: rng.Intn(10 * n), ID: fmt.Sprintf("id%d", i)})
		}
		// 固定宽度窗口查询（命中数期望恒定，与 n 无关）
		_, cmps, visited := ix.Range(domain.CatBoiler, 5000, 5009)
		t.Logf("n=%d 比较次数=%d 访问元素=%d", n, cmps, visited)
		if visited > 100 {
			t.Fatalf("n=%d 访问元素数 %d 超出命中量级", n, visited)
		}
		if n == sizes[len(sizes)-1] {
			// 对数增长上界：10 倍数据量下比较次数增长不得超过约 2 倍
			if cmps > 2*prevCmps+10 {
				t.Fatalf("比较次数随总量线性增长? n=1万:%d n=10万:%d", prevCmps, cmps)
			}
		}
		prevCmps, prevVisited = cmps, visited
		_ = prevVisited
	}
}
