package ontology

import (
	"fmt"
	"testing"
)

// mustRegister 构造一个带若干链接类型与实例的服务/朴素模型对。
// 默认声明：
//
//	LT1: src cap=2（对象类型 S），tgt cap=1（对象类型 T）
//	LT2: src ExactlyOne（S2），tgt Unlimited（T2）
func mustRegister(t *testing.T) (*Service, *naiveModel) {
	t.Helper()
	s := NewService()
	m := newNaiveModel()

	decls := []LinkTypeDecl{
		{
			Name: "LT1", SourceType: "S", TargetType: "T",
			SourceCap: CardinalityBound{Kind: AtMost, Max: 2},
			TargetCap: CardinalityBound{Kind: AtMostOne},
		},
		{
			Name: "LT2", SourceType: "S2", TargetType: "T2",
			SourceCap: CardinalityBound{Kind: ExactlyOne},
			TargetCap: CardinalityBound{Kind: Unlimited},
		},
	}
	for _, d := range decls {
		if err := s.RegisterLinkType(d); err != nil {
			t.Fatalf("register %s: %v", d.Name, err)
		}
		m.registerLinkType(d)
	}

	objs := map[string]string{
		"s1": "S", "s2": "S", "s3": "S",
		"t1": "T", "t2": "T", "t3": "T",
		"u1": "S2", "u2": "S2",
		"v1": "T2", "v2": "T2", "v3": "T2",
	}
	for id, typ := range objs {
		if err := s.RegisterObject(id, typ); err != nil {
			t.Fatalf("register object %s: %v", id, err)
		}
		m.registerObject(id, typ)
	}
	return s, m
}

func fmtItems(items []BatchItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprintf("%s:(%s->%s)", it.LinkType, it.Source, it.Target)
	}
	return out
}

func fmtResults(r *BatchResult) []string {
	out := make([]string, len(r.Results))
	for i, x := range r.Results {
		if x.Accepted {
			out[i] = "ACCEPTED"
		} else {
			out[i] = "REJECTED:" + x.Kind.String()
		}
	}
	return out
}

func sameSnapshot(a, b []StoredLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func resultsEqual(a, b *BatchResult) bool {
	if len(a.Results) != len(b.Results) {
		return false
	}
	for i := range a.Results {
		if a.Results[i] != b.Results[i] {
			return false
		}
	}
	return a.Aborted == b.Aborted && a.AbortIndex == b.AbortIndex
}
