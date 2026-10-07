package orphanreclaim

import "testing"

// 两层核对次序：先独立、后联合；联合单独存在不保留。
func TestTwoLayerOrdering(t *testing.T) {
	r, c, lg := newTestEngine(t, testConfig(), 1000)
	r.CreateObject("t")
	r.CreateObject("s")

	// 评估由写操作触发：先制造一条联合单边使其入队。
	mustAdd(t, r, "s", "t", "J1")
	assertGen(t, r, "t", Gen1)
	if d := lg.decisions[len(lg.decisions)-1]; d.Basis.Layer != "none" || !d.Orphan {
		t.Fatalf("joint-only single side must be orphan, got %+v", d.Basis)
	}

	// J2 补齐：联合组满足，脱离。
	r.CreateObject("probe")
	mustAdd(t, r, "probe", "t", "J2")
	assertGen(t, r, "t", GenNone)
	d := lg.decisions[len(lg.decisions)-1]
	if d.Basis.Layer != "joint" || len(d.Basis.LinkTypes) != 2 {
		t.Fatalf("want joint basis [J1 J2], got %+v", d.Basis)
	}

	// 撤掉 J1：联合破坏，重新从第一代起算。
	c.t = 2000
	mustRemove(t, r, "s", "t", "J1")
	assertGen(t, r, "t", Gen1)

	// 此时即使联合组不完整，只要出现独立类型即保留——
	// 验证「先核对独立层、命中即返回，不再关心联合层」。
	mustAdd(t, r, "s", "t", "I")
	assertGen(t, r, "t", GenNone)
	d = lg.decisions[len(lg.decisions)-1]
	if d.Basis.Layer != "independent" || d.Basis.LinkTypes[0] != "I" {
		t.Fatalf("want independent basis I, got %+v", d.Basis)
	}

	// 独立类型存在时，联合边全部撤光也仍然保留（第一层短路）。
	mustRemove(t, r, "probe", "t", "J2")
	orphan, err := r.IsOrphan("t")
	if err != nil || orphan {
		t.Fatalf("independent edge alone must retain: orphan=%v err=%v", orphan, err)
	}
}

// 不同联合组之间不串味：{J1,J2} 与 {X,Y} 不能交叉满足。
func TestJointGroupsDoNotCross(t *testing.T) {
	r, _, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("t")
	r.CreateObject("s")
	mustAdd(t, r, "s", "t", "J1")
	mustAdd(t, r, "s", "t", "X")
	orphan, _ := r.IsOrphan("t")
	if !orphan {
		t.Fatal("J1+X must not satisfy either joint group")
	}
	assertGen(t, r, "t", Gen1)
}
