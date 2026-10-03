package ontology

import (
	"math/bits"
	"testing"
)

// TestSingleSortCounter 用非导出计数器 sortCompares 验证 Auction 只对参拍者排一次序，
// 而不是每个广告位重复扫描全部竞价者。100 与 10000 两档。
//
// 判定依据：
//   - 单次排序（sort.Sort，模式判定快速排序）的比较次数上界约为 n*log2(n)；
//     取宽限上界 2*n*ceil(log2(n)+1)（覆盖模式判定快速排序的最坏常系数）。
//   - “每个广告位重复全量扫描”的朴素做法比较次数至少为 K*n 量级；
//     当 K 与 n 同档时必然超过该上界（如 n=100、K=100 时为 10000 次）。
//
// 因此计数器既证明实现只用一次排序，也使任何 K 轮全量扫描的回归无法通过测试。
func TestSingleSortCounter(t *testing.T) {
	for _, n := range []int{100, 10000} {
		e := NewEngine()
		for i := 0; i < n; i++ {
			// 让分值各不相同（bid 递升、q 固定），所有竞价者均参拍。
			mustReg(t, e, bidderName(i), int64(i+1), 1000, 1_000_000_000_000)
		}
		k := 100
		if n < k {
			k = n
		}
		ws, err := e.Auction(k, 1)
		if err != nil {
			t.Fatalf("n=%d auction: %v", n, err)
		}
		if len(ws) != k {
			t.Fatalf("n=%d winners = %d, want %d", n, len(ws), k)
		}
		compares := e.SortCompares()
		bound := 2 * int64(n) * int64(bits.Len64(uint64(n)))
		// 下界：排序 n 个互异元素至少需要 n-1 次比较（连通图下界）。
		if compares < int64(n)-1 {
			t.Fatalf("n=%d compares=%d below n-1, counter likely broken", n, compares)
		}
		if compares > bound {
			t.Fatalf("n=%d compares=%d > single-sort bound %d (per-slot rescan?)", n, compares, bound)
		}
		// 关键反证：K*n 量级（逐广告位全量扫描）会超过上界。
		if int64(k*n) <= bound {
			t.Fatalf("n=%d K=%d: test cannot distinguish per-slot scan (%d) from bound %d", n, k, k*n, bound)
		}
		t.Logf("判定依据 n=%d K=%d: 比较次数=%d，一次排序上界=%d，逐位扫描量级=%d => 只排序一次",
			n, k, compares, bound, k*n)
	}
}

// TestSingleSortCounterEligibleSubset：仅有部分竞价者参拍时，排序规模是参拍者集合。
func TestSingleSortCounterEligibleSubset(t *testing.T) {
	e := NewEngine()
	n := 200
	for i := 0; i < n; i++ {
		mustReg(t, e, bidderName(i), int64(i+1), 1000, 1_000_000_000_000)
	}
	// 重新构造引擎：小预算的 5 人在登记时即 a<bid，直接出局。
	e2 := NewEngine()
	eligible := 0
	for i := 0; i < n; i++ {
		bid := int64(i + 1)
		budget := int64(1_000_000_000_000)
		if bid >= 101 {
			eligible++
		}
		if i >= 195 { // 5 人预算不足 bid
			budget = bid - 1
		}
		mustReg(t, e2, bidderName(i), bid, 1000, budget)
	}
	eligible -= 5
	ws, err := e2.Auction(100, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != eligible {
		t.Fatalf("winners=%d, want eligible=%d", len(ws), eligible)
	}
	compares := e2.SortCompares()
	bound := 2 * int64(eligible) * int64(bits.Len64(uint64(eligible)))
	if compares > bound {
		t.Fatalf("compares=%d > eligible-sort bound %d", compares, bound)
	}
	t.Logf("判定依据 参拍者=%d（其余因 bid<P 或 a<bid 排除）：比较次数=%d，上界=%d",
		eligible, compares, bound)
}

func bidderName(i int) string {
	// 十进制定长，保证 map 遍历与名字无关；规则只允许按登记序号打破同分。
	const digits = "0123456789"
	if i == 0 {
		return "b00000"
	}
	var buf [6]byte
	for j := len(buf) - 1; j >= 0; j-- {
		buf[j] = digits[i%10]
		i /= 10
	}
	return "b" + string(buf[:])
}
