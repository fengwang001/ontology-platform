package instance

import (
	"fmt"
	"testing"

	"ontology/internal/bitemporal"
)

// TestQueryComplexityGuards 以可执行、不依赖人工读代码的方式证明：
// 双坐标定位的访问次数随单键版本数对数增长，而非线性增长；
// 且对“对象类型下实例总数”不敏感（多键共享存储，彼此零影响）。
//
// 做法：只通过 LastAsOf 返回的 visits 计数（树节点访问 + 链上二分步数），
// 不让测试遍历任何历史版本。若实现退化为线性扫描，访问数会逼近版本数，
// 下面的比率断言会立即失败。
func TestQueryComplexityGuards(t *testing.T) {
	s := New()

	// 1) 单键堆积大量不同业务起点（每次都带正确凭证，保证全部接受）。
	const n1 = 20000
	var base int64
	for i := 1; i <= n1; i++ {
		biz := int64(i * 10) // 互不相同的起点
		v, err := s.Write(WriteRequest{
			ObjectType: "T", PrimaryKey: "big", BizStart: biz, Base: base,
			Payload: fmt.Sprintf("p%d", i),
		})
		if err != nil {
			t.Fatalf("seed write %d: %v", i, err)
		}
		base = v.SysVersion
	}
	c := s.chains[key{objType: "T", pk: "big"}]

	measure := func(bizAt int64) int {
		var visits int
		_, ok := c.index.LastAsOf(s.Clock(), bizAt, &visits)
		if !ok {
			t.Fatalf("expected hit at biz=%d", bizAt)
		}
		return visits
	}
	v20k := measure(int64(n1*10 - 5))

	// 2) 再造一批“其它对象类型 + 大量无关主键”，验证与实例总数无关。
	for j := 0; j < 5000; j++ {
		_, err := s.Write(WriteRequest{
			ObjectType: "OtherType", PrimaryKey: fmt.Sprintf("noise%d", j),
			BizStart: 1, Payload: "noise",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	v20kAfterNoise := measure(int64(n1*10 - 5))

	// 3) 同业务起点的长覆盖链：访问 = 树深 + 链上二分。
	const n2 = 20000
	base = s.chains[key{objType: "T", pk: "big"}].commits[len(c.commits)-1].SysVersion
	for i := 0; i < n2; i++ {
		v, err := s.Write(WriteRequest{
			ObjectType: "T", PrimaryKey: "big", BizStart: 10, Base: base,
			Payload: "same-start",
		})
		if err != nil {
			t.Fatal(err)
		}
		base = v.SysVersion
	}
	chainVisits := measure(15) // 命中起点 10 的覆盖链

	t.Logf("COMPLEXITY distinctStarts=%d visits=%d ; after +5000 unrelated keys visits=%d ; same-start chain=%d visits=%d",
		n1, v20k, v20kAfterNoise, n2, chainVisits)

	// 对数护栏：2 万版本时访问必须在 ~log2(20000)≈15 的常数倍内。
	if v20k > 64 {
		t.Fatalf("distinct-start lookup visits=%d not logarithmic in %d versions", v20k, n1)
	}
	if v20kAfterNoise != v20k {
		t.Fatalf("lookup must be independent of instance count: %d vs %d", v20k, v20kAfterNoise)
	}
	if chainVisits > 64 {
		t.Fatalf("same-start chain visits=%d not logarithmic in chain %d", chainVisits, n2)
	}

	// 增长性断言：从 100 到 20000，访问数增速必须远低于版本数增速（200 倍）。
	small := buildAndMeasure(t, 100)
	large := buildAndMeasure(t, 20000)
	ratioVersions := 20000.0 / 100.0
	ratioVisits := float64(large) / float64(max(1, small))
	t.Logf("GROWTH versions x%.0f while visits x%.2f (%d -> %d)",
		ratioVersions, ratioVisits, small, large)
	if ratioVisits > 8 { // 允许常数因子，对数增长远小于 200 倍
		t.Fatalf("visits grew x%.2f, looks linear", ratioVisits)
	}
}

func buildAndMeasure(t *testing.T, starts int) int {
	t.Helper()
	var idx bitemporal.Index
	for i := 1; i <= starts; i++ {
		idx = idx.Apply(bitemporal.Commit{SysVersion: int64(i), BizStart: int64(i * 10)})
	}
	var visits int
	if _, ok := idx.LastAsOf(int64(starts), int64(starts*10-5), &visits); !ok {
		t.Fatal("expected hit")
	}
	return visits
}
