package pvbinding

import "testing"

func TestReclaimResetAndExpansion(t *testing.T) {
	for _, policy := range []ReclaimPolicy{ReclaimRetain, ReclaimDelete} {
		t.Run(string(policy), func(t *testing.T) {
			log := newOperationLog(t)
			controller := NewController()
			spec := testVolume("pv", 10, "c")
			spec.ReclaimPolicy = policy
			requireOK(t, controller, log, "CreateVolume(pv)", controller.CreateVolume(spec), "创建不同回收策略的卷")
			requireOK(t, controller, log, "CreateClaim(claim)", controller.CreateClaim(testClaim("claim", 5, "c", BindingImmediate)), "声明绑定到pv")
			requireOK(t, controller, log, "DeleteClaim(claim)", controller.DeleteClaim("claim"), "删除已绑定声明触发回收")
			if policy == ReclaimDelete {
				if _, ok := controller.GetVolume("pv"); ok {
					t.Fatal("delete policy must remove volume")
				}
				return
			}
			status, _ := controller.GetVolume("pv")
			if status.State != VolumeReleased || status.HasReservation {
				t.Fatalf("released status = %+v", status)
			}
			requireOK(t, controller, log, "CreateClaim(reused)", controller.CreateClaim(testClaim("reused", 1, "c", BindingImmediate)), "已释放卷不能作为候选，因此声明待绑定")
			if got := boundVolume(t, controller, "reused"); got != "" {
				t.Fatalf("released volume reused by %s", got)
			}
			requireOK(t, controller, log, "ResetVolume(pv)", controller.ResetVolume("pv"), "管理员显式重置后卷回到可用并触发重评估")
			if got := boundVolume(t, controller, "reused"); got != "pv" {
				t.Fatalf("reused got %s", got)
			}
		})
	}
}

func TestExpansionBoundaries(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	requireOK(t, controller, log, "CreateVolume(pv)", controller.CreateVolume(testVolume("pv", 10, "c")), "准备容量10的卷")
	requireOK(t, controller, log, "CreateClaim(claim)", controller.CreateClaim(testClaim("claim", 5, "c", BindingImmediate)), "创建容量5的已绑定声明")
	requireOK(t, controller, log, "ExpandClaim(claim,10)", controller.ExpandClaim("claim", 10), "扩到卷容量为边界成功")
	if status, _ := controller.GetClaim("claim"); status.RequestedCapacity != 10 {
		t.Fatalf("capacity = %d", status.RequestedCapacity)
	}
	requireCode(t, controller, log, "ExpandClaim(claim,11)", controller.ExpandClaim("claim", 11), ErrCodeInsufficientCapacity, "超过卷容量报容量不足且不改变状态")
	requireCode(t, controller, log, "ExpandClaim(claim,9)", controller.ExpandClaim("claim", 9), ErrCodeInvalidArgument, "降低容量为参数非法且不改变状态")
	requireOK(t, controller, log, "CreateClaim(pending)", controller.CreateClaim(testClaim("pending", 1, "other", BindingImmediate)), "无候选的待绑定声明")
	requireCode(t, controller, log, "ExpandClaim(pending,2)", controller.ExpandClaim("pending", 2), ErrCodeConflict, "待绑定声明扩容是状态冲突")
}

func TestDeletePendingClaimDoesNotTouchVolumes(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	requireOK(t, controller, log, "CreateVolume(pv)", controller.CreateVolume(testVolume("pv", 10, "c")), "创建唯一可用卷")
	requireOK(t, controller, log, "CreateClaim(pending)", controller.CreateClaim(testClaim("pending", 11, "c", BindingImmediate)), "容量不足，声明待绑定")
	requireOK(t, controller, log, "DeleteClaim(pending)", controller.DeleteClaim("pending"), "删除待绑定声明不影响任何卷")
	status, _ := controller.GetVolume("pv")
	if status.State != VolumeAvailable {
		t.Fatalf("volume state = %s", status.State)
	}
}

func TestErrorPriorityAndAtomicRejection(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	requireCode(t, controller, log, "ExpandClaim('',0)", controller.ExpandClaim("", 0), ErrCodeInvalidArgument, "参数非法优先于对象不存在")
	requireCode(t, controller, log, "ExpandClaim(missing,1)", controller.ExpandClaim("missing", 1), ErrCodeNotFound, "对象不存在优先于状态冲突")
	requireOK(t, controller, log, "CreateClaim(pending)", controller.CreateClaim(testClaim("pending", 1, "c", BindingImmediate)), "无卷时创建待绑定声明")
	requireCode(t, controller, log, "ExpandClaim(pending,1)", controller.ExpandClaim("pending", 1), ErrCodeConflict, "同值降低/扩容状态冲突")
	if status, _ := controller.GetClaim("pending"); status.RequestedCapacity != 1 {
		t.Fatalf("rejected operation changed capacity to %d", status.RequestedCapacity)
	}
}
