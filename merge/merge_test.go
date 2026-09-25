package merge

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/pagg"
)

// 不变量 2：同一批部分结果以任意分区完成顺序归并，输出逐字段相同。
func TestMergeDeterministicAcrossOrders(t *testing.T) {
	cases := [][][]pagg.Entry{
		{ // 第三节的六条记录
			{{Key: "a", Total: 8}},
			{{Key: "a", Total: 1}, {Key: "c", Total: 4}},
			{{Key: "a", Total: 2}, {Key: "b", Total: 6}},
		},
		{},              // 零分区
		{nil, nil, nil}, // 全空分区
	}
	for _, partials := range cases {
		want := fmt.Sprint(Merge(partials))
		for trial := 0; trial < 50; trial++ {
			perm := rand.Perm(len(partials))
			shuffled := make([][]pagg.Entry, 0, len(partials))
			for _, i := range perm {
				shuffled = append(shuffled, partials[i])
			}
			if got := fmt.Sprint(Merge(shuffled)); got != want {
				t.Fatalf("order %v: got %s, want %s", perm, got, want)
			}
		}
	}
}

// 复杂度：P 个分区各含一个互不相同的 key，找最小头的比较次数不得随 P 二次增长。
func TestMergeComparesSubQuadratic(t *testing.T) {
	for _, P := range []int{100, 300, 1000, 3000, 10000} {
		partials := make([][]pagg.Entry, P)
		for i := range partials {
			partials[i] = []pagg.Entry{{Key: fmt.Sprintf("k%05d", i), Total: 1}}
		}
		if got := len(Merge(partials)); got != P {
			t.Fatalf("P=%d: merged %d entries, want %d", P, got, P)
		}
		compares := lastCompares.Load()
		lg := 0 // ceil(log2 P)
		for n := P - 1; n > 0; n >>= 1 {
			lg++
		}
		bound := int64(3 * P * (lg + 1)) // O(P log P)：堆每次上浮/下沉 ≤ 2·log2P
		if compares > bound {
			t.Fatalf("P=%d: compares=%d exceeds O(P log P) bound %d (quadratic scan?)", P, compares, bound)
		}
	}
}
