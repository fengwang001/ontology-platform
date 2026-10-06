package pvbinding

import "testing"

// opLog 记录每次操作的输入、实际输出与判定依据，并打印到测试日志。
type opLog struct{ t *testing.T }

func newLog(t *testing.T) *opLog { return &opLog{t: t} }

func (l *opLog) step(input string, got interface{}, basis string) {
	l.t.Helper()
	l.t.Logf("[操作输入] %s\n  [实际输出] %v\n  [判定依据] %s", input, got, basis)
}

func TestImmediate_CandidateBoundaries(t *testing.T) {
	c := New()
	log := newLog(t)

	addV := func(name string, cap int64, sc string, vs VolumeSpec) {
		t.Helper()
		vs.Capacity = cap
		vs.StorageClass = sc
		vs.Reclaim = ReclaimRetain
		if len(vs.AccessModes) == 0 {
			vs.AccessModes = am(ReadWriteOnce)
		}
		err := c.AddVolume(name, vs)
		assertNoErr(t, c, err, "AddVolume:"+name)
		assertInvariants(t, c)
	}

	// 容量边界：恰好等于请求容量可候选。
	addV("v10", 10, "fast", VolumeSpec{})
	addV("v20", 20, "fast", VolumeSpec{})
	cs := claimSpec(10, "fast", am(ReadWriteOnce))
	err := c.AddClaim("c1", cs)
	log.step("AddClaim c1 req=10 sc=fast immediate", err, "容量恰好满足的 v10 应胜出")
	assertNoErr(t, c, err, "AddClaim c1")
	assertBound(t, c, "c1", "v10")

	// 存储类不同的卷不可候选：只有 slow 卷时声明保持待绑定。
	cs = claimSpec(5, "slow", am(ReadWriteOnce))
	err = c.AddClaim("c2", cs)
	log.step("AddClaim c2 sc=slow", err, "无 slow 卷 => 待绑定，错误为 nil")
	assertNoErr(t, c, err, "AddClaim c2")
	assertPending(t, c, "c2")
	assertInvariants(t, c)

	// 访问模式边界：卷缺少 RWX 不可候选，补齐后经新卷加入触发重评估。
	cs = claimSpec(5, "fast", am(ReadWriteOnce, ReadWriteMany))
	err = c.AddClaim("c3", cs)
	log.step("AddClaim c3 needs RWO+RWX", err, "现存 fast 卷无 RWX => 待绑定")
	assertPending(t, c, "c3")
	addV("v30", 30, "fast", VolumeSpec{AccessModes: am(ReadWriteOnce, ReadWriteMany)})
	log.step("AddVolume v30 modes=RWO,RWX", nil, "新卷加入触发重评估，c3 绑定 v30")
	assertBound(t, c, "c3", "v30")

	// 标签选择器边界：键值必须全部相等。
	addV("v-label-a", 10, "ssd", VolumeSpec{Labels: labels("zone", "a", "tier", "gold")})
	addV("v-label-b", 10, "ssd", VolumeSpec{Labels: labels("zone", "b", "tier", "gold")})
	cs = claimSpec(1, "ssd", am(ReadWriteOnce))
	cs.Selector = labels("zone", "b")
	err = c.AddClaim("c4", cs)
	log.step("AddClaim c4 selector zone=b", err, "只有 v-label-b 满足标签选择器")
	assertNoErr(t, c, err, "AddClaim c4")
	assertBound(t, c, "c4", "v-label-b")

	// 缺失标签键的卷不可候选。
	cs = claimSpec(1, "ssd", am(ReadWriteOnce))
	cs.Selector = labels("zone", "a", "rack", "r1")
	err = c.AddClaim("c5", cs)
	log.step("AddClaim c5 selector zone=a,rack=r1", err, "v-label-a 缺 rack 键 => 无候选 => 待绑定")
	assertPending(t, c, "c5")
	assertInvariants(t, c)
}

func TestImmediate_TieCapacityPicksLexicographic(t *testing.T) {
	c := New()
	log := newLog(t)
	assertNoErr(t, c, c.AddVolume("vz", volSpec(10, "x", am(ReadWriteOnce))), "vz")
	assertNoErr(t, c, c.AddVolume("va", volSpec(10, "x", am(ReadWriteOnce))), "va")
	assertNoErr(t, c, c.AddVolume("vm", volSpec(10, "x", am(ReadWriteOnce))), "vm")
	err := c.AddClaim("c1", claimSpec(1, "x", am(ReadWriteOnce)))
	log.step("AddClaim c1，三卷容量均为 10", err, "容量并列取名称字典序最小 va")
	assertNoErr(t, c, err, "AddClaim c1")
	assertBound(t, c, "c1", "va")
	assertInvariants(t, c)
}

func TestImmediate_ReevaluationOrder(t *testing.T) {
	c := New()
	log := newLog(t)
	// 先建两个待绑定声明，名称顺序 zzz < aaa 重评估应按名称升序。
	assertNoErr(t, c, c.AddClaim("zzz", claimSpec(5, "x", am(ReadWriteOnce))), "zzz")
	assertNoErr(t, c, c.AddClaim("aaa", claimSpec(5, "x", am(ReadWriteOnce))), "aaa")
	assertNoErr(t, c, c.AddVolume("only", volSpec(5, "x", am(ReadWriteOnce))), "only")
	log.step("先加 zzz、aaa（均待绑定），再加唯一满足卷 only", nil,
		"重评估按声明名称升序：aaa 先拿到 only，zzz 仍待绑定")
	assertBound(t, c, "aaa", "only")
	assertPending(t, c, "zzz")
	assertInvariants(t, c)

	// 卷被修改后重新评估：把另一个不可用的大容量卷规格改为可满足。
	assertNoErr(t, c, c.AddVolume("big", volSpec(100, "y", am(ReadWriteOnce))), "big")
	err := c.UpdateVolume("big", volSpec(6, "x", am(ReadWriteOnce)))
	log.step("UpdateVolume big: sc=y,cap=100 -> sc=x,cap=6", err,
		"卷修改后重评估，zzz 绑定修改后的 big")
	assertNoErr(t, c, err, "UpdateVolume big")
	assertBound(t, c, "zzz", "big")
	assertInvariants(t, c)
}

func TestImmediate_SpecifiedVolumeNoReselect(t *testing.T) {
	c := New()
	log := newLog(t)
	assertNoErr(t, c, c.AddVolume("target", volSpec(100, "x", am(ReadWriteOnce))), "target")
	assertNoErr(t, c, c.AddVolume("tiny", volSpec(1, "x", am(ReadWriteOnce))), "tiny")

	cs := claimSpec(50, "x", am(ReadWriteOnce))
	cs.VolumeName = "target"
	err := c.AddClaim("c1", cs)
	log.step("AddClaim c1 指定卷 target（另有更小的 tiny）", err,
		"指定卷名称时不得按最小容量改选，应绑定 target")
	assertNoErr(t, c, err, "AddClaim c1")
	assertBound(t, c, "c1", "target")

	// 指定卷不满足候选条件 => 保持待绑定，且不得改选其他卷。
	cs = claimSpec(1, "x", am(ReadWriteOnce))
	cs.VolumeName = "missing-vol"
	err = c.AddClaim("c2", cs)
	log.step("AddClaim c2 指定不存在的 missing-vol，tiny 可用", err,
		"指定卷不满足（不存在）=> 待绑定，绝不改选 tiny")
	assertPending(t, c, "c2")

	// 指定卷容量不足同样保持待绑定。
	cs = claimSpec(999, "x", am(ReadWriteOnce))
	cs.VolumeName = "tiny"
	err = c.AddClaim("c3", cs)
	log.step("AddClaim c3 指定 tiny 但 req=999", err,
		"指定卷容量不足 => 待绑定；其他卷加入也不得改选")
	assertPending(t, c, "c3")
	assertNoErr(t, c, c.AddVolume("huge", volSpec(2000, "x", am(ReadWriteOnce))), "huge")
	assertPending(t, c, "c3")
	log.step("AddVolume huge cap=2000", nil, "c3 仍只认 tiny，保持待绑定")
	assertInvariants(t, c)
}

func TestImmediate_ReservedClaim(t *testing.T) {
	c := New()
	log := newLog(t)

	// 预留给 c2 的卷，c1 不能拿。
	vs := volSpec(5, "x", am(ReadWriteOnce))
	vs.ReservedClaim = "c2"
	assertNoErr(t, c, c.AddVolume("reserved", vs), "reserved")
	err := c.AddClaim("c1", claimSpec(1, "x", am(ReadWriteOnce)))
	log.step("AddClaim c1，唯一卷预留给 c2", err, "预留名不匹配 => c1 待绑定")
	assertPending(t, c, "c1")

	err = c.AddClaim("c2", claimSpec(1, "x", am(ReadWriteOnce)))
	log.step("AddClaim c2（被预留方）", err, "名称匹配预留 => c2 绑定 reserved")
	assertNoErr(t, c, err, "AddClaim c2")
	assertBound(t, c, "c2", "reserved")
	assertPending(t, c, "c1")
	assertInvariants(t, c)

	// 先有声明、后加预绑定卷：AddVolume 触发重评估时形成绑定。
	assertNoErr(t, c, c.AddClaim("c3", claimSpec(1, "x", am(ReadWriteOnce))), "c3")
	vs = volSpec(5, "x", am(ReadWriteOnce))
	vs.ReservedClaim = "c3"
	err = c.AddVolume("reserved2", vs)
	log.step("AddVolume reserved2 预留给已存在的 c3", err, "重评估时 c3 绑定 reserved2")
	assertNoErr(t, c, err, "AddVolume reserved2")
	assertBound(t, c, "c3", "reserved2")
	assertPending(t, c, "c1")
	assertInvariants(t, c)
}
