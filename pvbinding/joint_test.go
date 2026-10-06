package pvbinding

import "testing"

func delayed(cap int64, sc string) ClaimSpec {
	s := claimSpec(cap, sc, am(ReadWriteOnce))
	s.BindMode = BindWaitForConsumer
	return s
}

func mapAssignments(as []Assignment) map[string]string {
	m := make(map[string]string, len(as))
	for _, a := range as {
		m[a.ClaimName] = a.VolumeName
	}
	return m
}

func TestDelayed_BasicAndNodeConstraint(t *testing.T) {
	c := New()
	log := newLog(t)
	assertNoErr(t, c, c.AddClaim("d1", delayed(1, "x")), "d1")
	assertNoErr(t, c, c.AddVolume("v1", volSpec(5, "x", am(ReadWriteOnce))), "v1")
	log.step("延迟声明 d1 与卷 v1 同时存在", nil, "延迟绑定提交后不选卷，d1 保持待绑定")
	assertPending(t, c, "d1")

	// 卷仅允许 node-a：在 node-b 上联合绑定必须拒绝。
	vs := volSpec(5, "x", am(ReadWriteOnce))
	vs.NodeNames = ss("node-a")
	assertNoErr(t, c, c.AddVolume("v-node", vs), "v-node")
	_, err := c.JointBind(JointBindRequest{Node: "node-b", ClaimNames: []string{"d1"}})
	log.step("JointBind node=node-b claims=[d1]", err, "d1 仅能用 v1；v-node 不允许 node-b => 可成功")
	// v1 无节点约束，d1 在 node-b 上仍可使用 v1。
	assertNoErr(t, c, err, "JointBind node-b")
	assertInvariants(t, c)

	assertNoErr(t, c, c.AddClaim("d2", delayed(1, "x")), "d2")
	_, err = c.JointBind(JointBindRequest{Node: "node-b", ClaimNames: []string{"d2"}})
	log.step("JointBind node=node-b claims=[d2]，只剩 v-node", err,
		"v-node 节点约束不含 node-b => 无满足的卷，整体拒绝")
	assertKind(t, err, ErrNoMatch)
	assertPending(t, c, "d2")

	as, err := c.JointBind(JointBindRequest{Node: "node-a", ClaimNames: []string{"d2"}})
	log.step("JointBind node=node-a claims=[d2]", as, "node-a 在约束集合内 => 绑定 v-node")
	assertNoErr(t, c, err, "JointBind node-a")
	if mapAssignments(as)["d2"] != "v-node" {
		t.Fatalf("d2 should bind v-node, got %v", as)
	}
	assertInvariants(t, c)
}

func TestDelayed_GreedyFailsButJointSucceeds(t *testing.T) {
	c := New()
	log := newLog(t)

	// c-a 仅可使用卷 shared（另一个 vol-big 也可，但我们用节点约束构造：
	// 这里采用经典反贪心结构：按名称升序处理时，若 c-a 贪心选 vol-big
	// 会导致 c-b 无卷；整体最优指派是 c-a->shared, c-b->vol-big）。
	// 声明均请求 1：
	//   shared cap=10 仅对 c-a,c-b 都可用；
	//   big    cap=100 两者都可用。
	// 为制造"贪心逐个选取会失败"，令 c-a 只能用 shared，而 c-b 两个都可用。
	vsShared := volSpec(10, "x", am(ReadWriteOnce))
	vsShared.Labels = labels("k", "shared")
	vsBig := volSpec(100, "x", am(ReadWriteOnce))
	vsBig.Labels = labels("k", "big")
	assertNoErr(t, c, c.AddVolume("shared", vsShared), "shared")
	assertNoErr(t, c, c.AddVolume("big", vsBig), "big")

	ca := delayed(1, "x")
	ca.Selector = labels("k", "shared") // c-a 只能用 shared
	cb := delayed(1, "x")               // c-b 两者皆可
	assertNoErr(t, c, c.AddClaim("c-a", ca), "c-a")
	assertNoErr(t, c, c.AddClaim("c-b", cb), "c-b")

	as, err := c.JointBind(JointBindRequest{Node: "node-1", ClaimNames: []string{"c-b", "c-a"}})
	log.step("JointBind claims 输入乱序 [c-b,c-a]，c-a 仅能用 shared", as,
		"整体可行指派存在 => 必须成功：c-a->shared, c-b->big；按名称排序后求解")
	assertNoErr(t, c, err, "JointBind")
	m := mapAssignments(as)
	if m["c-a"] != "shared" || m["c-b"] != "big" {
		t.Fatalf("assignment = %v, want c-a=shared c-b=big", m)
	}
	assertInvariants(t, c)

	// 真正的贪心失败结构：c-a 候选只有 shared；c-b 候选 {shared,big}，
	// 若逐个贪心按 c-b 先选最小浪费 shared，则 c-a 无卷。联合求解必须避开。
	c2 := New()
	assertNoErr(t, c2, c2.AddVolume("shared", vsShared), "shared")
	assertNoErr(t, c2, c2.AddVolume("big", vsBig), "big")
	assertNoErr(t, c2, c2.AddClaim("a", ca), "a") // a 仅 shared
	assertNoErr(t, c2, c2.AddClaim("b", cb), "b") // b 任意
	as, err = c2.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"a", "b"}})
	log.step("反贪心：a 仅 shared，b 若贪心拿 shared 则 a 失败", as,
		"穷举指派保证 a->shared, b->big，总浪费 9+99=108 是唯一可行解")
	assertNoErr(t, c2, err, "JointBind2")
	m = mapAssignments(as)
	if m["a"] != "shared" || m["b"] != "big" {
		t.Fatalf("assignment = %v, want a=shared b=big", m)
	}
	assertInvariants(t, c2)
}

func TestDelayed_AllOrNothing(t *testing.T) {
	c := New()
	log := newLog(t)
	assertNoErr(t, c, c.AddVolume("v1", volSpec(5, "x", am(ReadWriteOnce))), "v1")
	assertNoErr(t, c, c.AddClaim("d1", delayed(1, "x")), "d1")
	assertNoErr(t, c, c.AddClaim("d2", delayed(1, "x")), "d2")
	_, err := c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"d1", "d2"}})
	log.step("JointBind [d1,d2] 但只有一个卷", err,
		"需要互不相同的卷，无可行指派 => 拒绝且不改变任何状态")
	assertKind(t, err, ErrNoMatch)
	assertPending(t, c, "d1")
	assertPending(t, c, "d2")
	assertVolumePhase(t, c, "v1", VolumeAvailable, true)
	assertInvariants(t, c)
}

func TestDelayed_WasteTieLexOrder(t *testing.T) {
	c := New()
	log := newLog(t)
	// 两声明请求均为 5；两个卷容量均为 5 => 总浪费并列 0。
	assertNoErr(t, c, c.AddVolume("vz", volSpec(5, "x", am(ReadWriteOnce))), "vz")
	assertNoErr(t, c, c.AddVolume("va", volSpec(5, "x", am(ReadWriteOnce))), "va")
	assertNoErr(t, c, c.AddClaim("c1", delayed(5, "x")), "c1")
	assertNoErr(t, c, c.AddClaim("c2", delayed(5, "x")), "c2")
	as, err := c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"c2", "c1"}})
	log.step("两卷容量相同、两种指派总浪费相同", as,
		"按声明名升序比较卷名：c1->va,c2->vz 字典序更小")
	assertNoErr(t, c, err, "JointBind")
	m := mapAssignments(as)
	if m["c1"] != "va" || m["c2"] != "vz" {
		t.Fatalf("tie assignment = %v, want c1=va c2=vz", m)
	}
	assertInvariants(t, c)
}

func TestDelayed_MinTotalWaste(t *testing.T) {
	c := New()
	log := newLog(t)
	// x req=9, y req=14；卷 a cap=10, b cap=20。
	// x->a,y->b: (10-9)+(20-14)=1+6=7
	// x->b,y->a: (20-9)+(10-14) 不合法：a 容量 10 < y 请求 14，所以只有一种可行指派。
	// 为让两种指派都可行且浪费严格不同：x req=6,y req=9：
	// x->a,y->b: 4+11=15；x->b,y->a: 14+1=15 仍并列。采用 y req=11：
	// x->a,y->b: 4+9=13；x->b,y->a: 14+0=14 => 取 13。
	assertNoErr(t, c, c.AddVolume("a", volSpec(10, "x", am(ReadWriteOnce))), "a")
	assertNoErr(t, c, c.AddVolume("b", volSpec(20, "x", am(ReadWriteOnce))), "b")
	assertNoErr(t, c, c.AddClaim("x", delayed(6, "x")), "x")
	assertNoErr(t, c, c.AddClaim("y", delayed(11, "x")), "y")
	as, err := c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"x", "y"}})
	log.step("x req=6,y req=11,a cap=10,b cap=20", as,
		"x->a,y->b 总浪费 13；x->b,y->a 总浪费 14 => 取严格最小者")
	assertNoErr(t, c, err, "JointBind")
	m := mapAssignments(as)
	if m["x"] != "a" || m["y"] != "b" {
		t.Fatalf("min waste assignment = %v, want x=a y=b", m)
	}
	assertInvariants(t, c)
}
