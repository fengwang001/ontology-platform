package compat

import "testing"

var (
	v3 = v(3, 0, 0)
	v4 = v(4, 0, 0)
)

// fourChain 构造 v1 -> v2 -> v3 -> v4 的链：
// v2 新增可选属性 note；v3 将 note 改为必填；v4 删除 qty。
func fourChain() []Format {
	return buildChain(genesisSchema(), []Version{v1, v2, v3, v4}, [][]Change{
		{{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType()}},
		{{ObjectType: "Order", Property: "note", Kind: ChangeRequireProperty}},
		{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty, Type: intType(FloatPtr(0), FloatPtr(100))}},
	})
}

func TestChain_EachStepHasOwnVerdict(t *testing.T) {
	chain := fourChain()
	p := readProfile(v1, fullRange())
	cv := JudgePath(p, chain, snapAt(v4, nil))
	if len(cv.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(cv.Steps))
	}
	for i, s := range cv.Steps {
		if s.To != chain[i+1].Version {
			t.Errorf("step %d To = %v, want %v", i, s.To, chain[i+1].Version)
		}
	}
	// 最后一级删除 qty 且消费方仍依赖 -> 不兼容。
	assertVerdict(t, cv.Final, LevelIncompatible, CatRequiredMissing)
}

func TestChain_StopsAtFirstFailure(t *testing.T) {
	// 写入场景：v3 把 note 改为必填即应失败，判定不得继续到 v4。
	chain := fourChain()
	p := readProfile(v1, fullRange())
	p.Mode = ModeWrite
	cv := JudgePath(p, chain, snapAt(v4, nil))
	if len(cv.Steps) != 2 {
		t.Fatalf("expected to stop at step 2, got %d steps", len(cv.Steps))
	}
	assertVerdict(t, cv.Steps[0].Verdict, LevelCompatible, CatNone)
	assertVerdict(t, cv.Steps[1].Verdict, LevelIncompatible, CatRequiredMissing)
	assertVerdict(t, cv.Final, LevelIncompatible, CatRequiredMissing)
}

func TestChain_NoTransitiveInference(t *testing.T) {
	// 消费方(v1) 对中间版本 v2 兼容、v2 对 v3 也兼容，
	// 但消费方对 v3 不兼容：传递推断不成立，必须逐级判定。
	chain := buildChain(genesisSchema(), []Version{v1, v2, v3}, [][]Change{
		{{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType()}},
		{{ObjectType: "Order", Property: "note", Kind: ChangeRequireProperty}},
	})
	p := readProfile(v1, fullRange())
	p.Mode = ModeWrite

	// 逐级核对：第一级兼容，第二级不兼容。
	cv := JudgePath(p, chain, snapAt(v3, nil))
	if len(cv.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(cv.Steps))
	}
	assertVerdict(t, cv.Steps[0].Verdict, LevelCompatible, CatNone)
	assertVerdict(t, cv.Steps[1].Verdict, LevelIncompatible, CatRequiredMissing)
	assertVerdict(t, cv.Final, LevelIncompatible, CatRequiredMissing)

	// 若只做相邻两两判定（v1~v2 兼容、v2~v3 兼容）会错误得出兼容结论；
	// 这里直接验证 v2 消费方对 v3 兼容，反衬传递推断的谬误。
	p2 := readProfile(v2, fullRange())
	p2.Mode = ModeWrite
	assertVerdict(t, Judge(p2, chain, snapAt(v3, nil)), LevelIncompatible, CatRequiredMissing)
}

func TestChain_BackwardDirection(t *testing.T) {
	// 消费方按 v4 编写，读取 v1 的历史快照：逐级反向核对。
	chain := fourChain()
	p := readProfile(v4, fullRange())
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 1}},
	})
	cv := JudgePath(p, chain, snap)
	// 第一级 v4->v3：qty 在数据侧重新出现（可选），兼容；
	// 第二级 v3->v2：note 在数据侧变为非必填，而消费方要求必填，
	// 且 v1 历史数据中不存在 note -> 必填缺失，判定停止。
	if len(cv.Steps) != 2 {
		t.Fatalf("expected to stop at step 2, got %d steps", len(cv.Steps))
	}
	assertVerdict(t, cv.Steps[0].Verdict, LevelCompatible, CatNone)
	assertVerdict(t, cv.Final, LevelIncompatible, CatRequiredMissing)
}

func TestChain_SameVersion(t *testing.T) {
	chain := fourChain()
	cv := JudgePath(readProfile(v2, fullRange()), chain, snapAt(v2, nil))
	if len(cv.Steps) != 0 {
		t.Fatalf("expected 0 steps, got %d", len(cv.Steps))
	}
	assertVerdict(t, cv.Final, LevelCompatible, CatNone)
}

func TestChain_IntermediateCounts(t *testing.T) {
	// 中间版本数量不同（0/1/2）时，步数随之变化且结论一致。
	for _, tc := range []struct {
		name     string
		versions []Version
		changes  [][]Change
		steps    int
	}{
		{"no-intermediate", []Version{v1, v2}, [][]Change{nil}, 1},
		{"one-intermediate", []Version{v1, v2, v3}, [][]Change{nil, nil}, 2},
		{"two-intermediates", []Version{v1, v2, v3, v4}, [][]Change{nil, nil, nil}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildChain(genesisSchema(), tc.versions, tc.changes)
			last := tc.versions[len(tc.versions)-1]
			cv := JudgePath(readProfile(v1, fullRange()), chain, snapAt(last, nil))
			if len(cv.Steps) != tc.steps {
				t.Errorf("steps = %d, want %d", len(cv.Steps), tc.steps)
			}
			assertVerdict(t, cv.Final, LevelCompatible, CatNone)
		})
	}
}
