package ontology

import (
	"strconv"
	"testing"
)

// TestCheckCostIndependentOfTotalLinks 以可机器验证的方式证明：
// 校验一条待创建链接的计数器访问次数恒为 2（两端各一次），
// 不随该对象类型下链接总数增长而增长。
//
// 朴素模型每次校验重扫全量集合（O(总链接数)）；账本实现的探针访问数
// 在 10 与 1000 条链接两种规模下必须完全相同。
func TestCheckCostIndependentOfTotalLinks(t *testing.T) {
	measure := func(n int) int {
		p := &probeCounter{}
		s := newServiceWithProbe(p)
		if err := s.RegisterLinkType(LinkTypeDecl{
			Name: "LT", SourceType: "S", TargetType: "T",
			SourceCap: CardinalityBound{Kind: Unlimited},
			TargetCap: CardinalityBound{Kind: Unlimited},
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			src := "s" + strconv.Itoa(i)
			tgt := "t" + strconv.Itoa(i)
			if err := s.RegisterObject(src, "S"); err != nil {
				t.Fatal(err)
			}
			if err := s.RegisterObject(tgt, "T"); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateLink("LT", Pair{src, tgt}); err != nil {
				t.Fatalf("预置链接失败: %v", err)
			}
		}
		before := p.accesses
		// 对一条新链接做一次必然走到基数判定的校验（类型无限不会拒绝）。
		if err := s.RegisterObject("probeS", "S"); err != nil {
			t.Fatal(err)
		}
		if err := s.RegisterObject("probeT", "T"); err != nil {
			t.Fatal(err)
		}
		_ = s.CreateLink("LT", Pair{"probeS", "probeT"})
		return p.accesses - before
	}

	costSmall := measure(10)
	costLarge := measure(1000)
	t.Logf("规模=10条链接  单次创建的计数器访问=%d", costSmall)
	t.Logf("规模=1000条链接 单次创建的计数器访问=%d", costLarge)
	t.Logf("判定依据=两次访问数必须都等于2（每端一次map读取），与链接总数无关")
	if costSmall != 2 || costLarge != 2 {
		t.Fatalf("校验成本不是常数：small=%d large=%d", costSmall, costLarge)
	}
}
