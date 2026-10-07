package ontology

import (
	"fmt"
	"testing"
)

// TestCheckCountIndependentOfSystemSize 证明权限检查次数只与动作
// 实际声明与实际触及的对象类型及实例数量相关，不随系统中对象类型、
// 链接类型或实例的总规模增长。证明手段是可观测的计数器与判定日志，
// 不依赖任何实现细节。
func TestCheckCountIndependentOfSystemSize(t *testing.T) {
	build := func(extra int) (*Store, *CountingAuthorizer, *Engine) {
		s := baseStore()
		putA(s, "a0")
		putB(s, "b1")
		putB(s, "b2")
		s.AddLink(Link{Type: "ab", From: "a0", To: "b1"})
		s.AddLink(Link{Type: "ab", From: "a0", To: "b2"})
		// 注入与动作无关的大规模对象类型、链接类型与实例。
		for i := 0; i < extra; i++ {
			tid := ObjectTypeID(fmt.Sprintf("X%d", i))
			s.AddObjectType(ObjectType{ID: tid, Properties: []string{"p"}})
			lid := LinkTypeID(fmt.Sprintf("LX%d", i))
			s.AddLinkType(LinkType{ID: lid, From: tid, To: tid})
			s.PutInstance(&Instance{ID: InstanceID(fmt.Sprintf("x%d", i)), Type: tid, Props: map[string]string{}, Version: 1})
		}
		counter := NewCountingAuthorizer(allowAll())
		eng := NewEngine(s, counter)
		eng.RegisterAction(modifyDecl(1, InvisibleSkip, MergeAll))
		return s, counter, eng
	}

	run := func(extra int) (int, int) {
		_, counter, eng := build(extra)
		allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
		if err != nil || !allowed {
			t.Fatalf("extra=%d: want allowed, got allowed=%v err=%v", extra, allowed, err)
		}
		recs := eng.Log()
		return counter.Total(), recs[len(recs)-1].CheckCount
	}

	small, smallLogged := run(0)
	large, largeLogged := run(2000)

	// 上界只由声明与触及规模决定：1 个直接目标 + 2 个级联实例，
	// 每个实例 1 次可见性 + 1 次授权，共 6 次。
	const want = 6
	if small != want {
		t.Fatalf("small system: %d checks, want %d", small, want)
	}
	if large != small {
		t.Fatalf("check count grew with system size: small=%d large=%d", small, large)
	}
	if smallLogged != small || largeLogged != large {
		t.Fatalf("logged check count mismatch: small=%d/%d large=%d/%d", small, smallLogged, large, largeLogged)
	}
}
