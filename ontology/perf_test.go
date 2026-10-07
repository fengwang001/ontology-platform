package ontology

import (
	"fmt"
	"testing"
)

// 性能要求（与规模无关）：判定给定主体对给定目标类型的最终权限
// 所考察的传播状态/边数量，不随平台内链接类型总数或对象类型
// 总数线性增长。本测试在可达子图不变、平台总规模放大 30 倍的
// 两个场景下断言判定考察量完全相等，从而可验证地证明该性质。
func TestDecisionWorkIndependentOfPlatformSize(t *testing.T) {
	build := func(extraTypes int) *Gateway {
		g := NewGateway(Config{})
		// 固定的可达子图：S -> M -> T。
		addTypes(g, "S", "M", "T")
		mustLink(t, g, "sm", "S", "M", 2)
		mustLink(t, g, "mt", "M", "T", 2)
		mustGrant(t, g, "alice", "S", "read")

		// 与 alice 无关的庞大区域：大量类型、链接与其他主体的授权。
		for i := 0; i < extraTypes; i++ {
			id := ObjectTypeID(fmt.Sprintf("X%d", i))
			g.AddObjectType(id)
			if i > 0 {
				prev := ObjectTypeID(fmt.Sprintf("X%d", i-1))
				mustLink(t, g, LinkTypeID(fmt.Sprintf("x%d", i)), prev, id, 3)
			}
			mustGrant(t, g, SubjectID(fmt.Sprintf("u%d", i)), id, "read")
		}
		return g
	}

	small := build(100)
	large := build(3000)

	decSmall, err := decide(t, small, "alice", "T")
	mustAllow(t, decSmall, err, "read")
	decLarge, err := decide(t, large, "alice", "T")
	mustAllow(t, decLarge, err, "read")

	if decSmall.Stats != decLarge.Stats {
		t.Fatalf("decision work grows with platform size: small=%+v large=%+v",
			decSmall.Stats, decLarge.Stats)
	}
	t.Logf("decision work constant across 30x platform growth: %+v", decLarge.Stats)
}
