package pvbinding

import "testing"

func TestReclaim_RetainAndDelete(t *testing.T) {
	log := newLog(t)

	// Retain：删除绑定声明后卷 Released，清空预留，永远不可再被选中，
	// 直到管理员 ResetVolume 才回到 Available。
	c := New()
	vs := volSpec(10, "x", am(ReadWriteOnce))
	vs.Reclaim = ReclaimRetain
	assertNoErr(t, c, c.AddVolume("ret", vs), "ret")
	assertNoErr(t, c, c.AddClaim("c1", claimSpec(1, "x", am(ReadWriteOnce))), "c1")
	assertBound(t, c, "c1", "ret")

	assertNoErr(t, c, c.AddClaim("c2", claimSpec(1, "x", am(ReadWriteOnce))), "c2")
	assertPending(t, c, "c2")
	err := c.DeleteClaim("c1")
	log.step("DeleteClaim c1（ret 策略 Retain）", err,
		"ret 转 Released 且清空 claimName；c2 重评估也不能复用 Released 卷")
	assertNoErr(t, c, err, "DeleteClaim c1")
	assertVolumePhase(t, c, "ret", VolumeReleased, true)
	assertPending(t, c, "c2")
	assertInvariants(t, c)

	snap := c.Snapshot()
	if snap.Volumes["ret"].Spec.ReservedClaim != "" || snap.Volumes["ret"].ClaimName != "" {
		t.Fatalf("retained volume must have reservation cleared: %+v", snap.Volumes["ret"])
	}

	// 新卷加入不应让 c2 拿到 released 卷；重置后才可复用。
	err = c.ResetVolume("ret")
	log.step("ResetVolume ret", err, "Released -> Available，重评估使 c2 绑定 ret")
	assertNoErr(t, c, err, "ResetVolume ret")
	assertBound(t, c, "c2", "ret")
	assertInvariants(t, c)

	// Delete 策略：删除声明即移除卷。
	c2 := New()
	vs2 := volSpec(10, "x", am(ReadWriteOnce))
	vs2.Reclaim = ReclaimDelete
	assertNoErr(t, c2, c2.AddVolume("del", vs2), "del")
	assertNoErr(t, c2, c2.AddClaim("d1", claimSpec(1, "x", am(ReadWriteOnce))), "d1")
	assertBound(t, c2, "d1", "del")
	err = c2.DeleteClaim("d1")
	log.step("DeleteClaim d1（del 策略 Delete）", err, "del 随即从系统移除")
	assertNoErr(t, c2, err, "DeleteClaim d1")
	assertVolumePhase(t, c2, "del", "", false)
	assertInvariants(t, c2)

	// 删除待绑定声明不影响任何卷。
	c3 := New()
	assertNoErr(t, c3, c3.AddVolume("v", volSpec(10, "x", am(ReadWriteOnce))), "v")
	assertNoErr(t, c3, c3.AddClaim("p", claimSpec(99, "x", am(ReadWriteOnce))), "p")
	assertPending(t, c3, "p")
	err = c3.DeleteClaim("p")
	log.step("DeleteClaim p（待绑定）", err, "卷 v 保持 Available，状态不变")
	assertNoErr(t, c3, err, "DeleteClaim p")
	assertVolumePhase(t, c3, "v", VolumeAvailable, true)
	assertInvariants(t, c3)
}

func TestExpand_Boundaries(t *testing.T) {
	c := New()
	log := newLog(t)
	assertNoErr(t, c, c.AddVolume("v10", volSpec(10, "x", am(ReadWriteOnce))), "v10")
	assertNoErr(t, c, c.AddClaim("c1", claimSpec(4, "x", am(ReadWriteOnce))), "c1")

	err := c.ExpandClaim("c1", 10)
	log.step("ExpandClaim c1 4 -> 10（恰好等于卷容量）", err, "边界值等于容量 => 成功")
	assertNoErr(t, c, err, "expand to 10")
	assertInvariants(t, c)

	err = c.ExpandClaim("c1", 11)
	log.step("ExpandClaim c1 10 -> 11（超过卷容量）", err, "容量不足错误，状态不变")
	assertKind(t, err, ErrCapacityExceeded)
	if c.Snapshot().Claims["c1"].Spec.RequestCapacity != 10 {
		t.Fatalf("rejected expand must not change state")
	}

	err = c.ExpandClaim("c1", 9)
	log.step("ExpandClaim c1 10 -> 9（降低）", err, "降低不允许 => 参数非法")
	assertKind(t, err, ErrInvalidArgument)

	// 待绑定声明扩容 => 状态冲突。
	assertNoErr(t, c, c.AddClaim("pending", claimSpec(1, "x", am(ReadWriteOnce))), "pending")
	err = c.ExpandClaim("pending", 2)
	log.step("ExpandClaim pending（待绑定）", err, "未绑定 => 状态冲突")
	assertKind(t, err, ErrConflict)
	assertInvariants(t, c)
}

func TestErrorKindPriority(t *testing.T) {
	c := New()
	log := newLog(t)

	// 参数非法 > 对象不存在：对不存在的声明传非法容量。
	err := c.ExpandClaim("ghost", -3)
	log.step("ExpandClaim ghost -3", err, "容量非正先判定为参数非法，而非不存在")
	assertKind(t, err, ErrInvalidArgument)

	// 对象不存在 > 状态冲突：删除不存在的声明。
	err = c.DeleteClaim("ghost")
	log.step("DeleteClaim ghost", err, "对象不存在")
	assertKind(t, err, ErrNotFound)

	// JointBind：非法参数（非延迟模式）优先于不存在。
	assertNoErr(t, c, c.AddVolume("v", volSpec(10, "x", am(ReadWriteOnce))), "v")
	assertNoErr(t, c, c.AddClaim("imm", claimSpec(1, "x", am(ReadWriteOnce))), "imm")
	_, err = c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"imm", "ghost"}})
	log.step("JointBind [imm(立即模式), ghost(不存在)]", err,
		"能判定的参数非法（非延迟）优先于不存在")
	assertKind(t, err, ErrInvalidArgument)

	// 已绑定（冲突）在存在性之后、无匹配之前。
	assertNoErr(t, c, c.AddClaim("dl", delayed(1, "x")), "dl")
	assertNoErr(t, c, c.AddVolume("v2", volSpec(10, "x", am(ReadWriteOnce))), "v2")
	_, err = c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"dl"}})
	assertNoErr(t, c, err, "bind dl first")
	_, err = c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"dl"}})
	log.step("JointBind [dl 已绑定的延迟声明]", err, "已绑定 => 状态冲突")
	assertKind(t, err, ErrConflict)

	// 重复声明名 => 参数非法。
	_, err = c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"a", "a"}})
	log.step("JointBind [a,a]", err, "重复引用 => 参数非法")
	assertKind(t, err, ErrInvalidArgument)

	// 空节点/空列表 => 参数非法。
	_, err = c.JointBind(JointBindRequest{Node: "", ClaimNames: []string{"a"}})
	assertKind(t, err, ErrInvalidArgument)
	_, err = c.JointBind(JointBindRequest{Node: "n", ClaimNames: nil})
	assertKind(t, err, ErrInvalidArgument)
	log.step("JointBind 空节点/空列表", err, "参数非法")

	// 冲突类：重复添加同名卷/声明。
	err = c.AddVolume("v", volSpec(1, "x", am(ReadWriteOnce)))
	assertKind(t, err, ErrConflict)
	err = c.AddClaim("imm", claimSpec(1, "x", am(ReadWriteOnce)))
	assertKind(t, err, ErrConflict)

	// 修改/重置状态不对的卷。
	err = c.UpdateVolume("v", volSpec(11, "x", am(ReadWriteOnce)))
	assertKind(t, err, ErrConflict)
	err = c.ResetVolume("v")
	assertKind(t, err, ErrConflict)
	err = c.UpdateVolume("ghost", volSpec(1, "x", am(ReadWriteOnce)))
	assertKind(t, err, ErrNotFound)

	// 非法规格。
	err = c.AddVolume("bad", VolumeSpec{Capacity: 0, StorageClass: "x",
		AccessModes: am(ReadWriteOnce), Reclaim: ReclaimRetain})
	assertKind(t, err, ErrInvalidArgument)
	err = c.AddClaim("bad", ClaimSpec{RequestCapacity: 1, AccessModes: am(ReadWriteOnce),
		BindMode: BindImmediate})
	assertKind(t, err, ErrInvalidArgument)
	assertInvariants(t, c)
}

func TestRejectedOpChangesNothing(t *testing.T) {
	c := New()
	assertNoErr(t, c, c.AddVolume("v1", volSpec(10, "x", am(ReadWriteOnce))), "v1")
	assertNoErr(t, c, c.AddClaim("c1", delayed(5, "x")), "c1")
	before := c.Snapshot()
	_, err := c.JointBind(JointBindRequest{Node: "n", ClaimNames: []string{"c1", "ghost"}})
	assertKind(t, err, ErrNotFound)
	after := c.Snapshot()
	if snapshotsEqual(before, after) != "" {
		t.Fatalf("state changed after rejected op: %s", snapshotsEqual(before, after))
	}
	assertInvariants(t, c)
}

func snapshotsEqual(a, b Snapshot) string {
	if len(a.Volumes) != len(b.Volumes) || len(a.Claims) != len(b.Claims) {
		return "length differs"
	}
	for n, v := range a.Volumes {
		w := b.Volumes[n]
		if w == nil || v.Phase != w.Phase || v.ClaimName != w.ClaimName ||
			v.Spec.Capacity != w.Spec.Capacity || v.Spec.ReservedClaim != w.Spec.ReservedClaim {
			return "volume differs: " + n
		}
	}
	for n, cl := range a.Claims {
		w := b.Claims[n]
		if w == nil || cl.Bound != w.Bound || cl.VolumeName != w.VolumeName ||
			cl.Spec.RequestCapacity != w.Spec.RequestCapacity {
			return "claim differs: " + n
		}
	}
	return ""
}
