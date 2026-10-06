package pvbinding

import "testing"

func TestDelayedJointAssignment(t *testing.T) {
	t.Run("greedy fails but joint assignment succeeds", func(t *testing.T) {
		log := newOperationLog(t)
		controller := NewController()
		pvB := testVolume("pv-b", 1, "c")
		pvB.AccessModes = []string{"RWO", "ROX"}
		pvC := testVolume("pv-c", 1, "c")
		for _, spec := range []VolumeSpec{pvB, pvC} {
			requireOK(t, controller, log, "CreateVolume("+spec.Name+")", controller.CreateVolume(spec), "构造共享候选卷")
		}
		claimA := testClaim("a", 1, "c", BindingDelayed)
		claimB := testClaim("b", 1, "c", BindingDelayed)
		claimB.AccessModes = []string{"ROX"}
		for _, spec := range []ClaimSpec{claimA, claimB} {
			requireOK(t, controller, log, "CreateClaim("+spec.Name+")", controller.CreateClaim(spec), "延迟声明提交后保持待绑定")
		}
		requireOK(t, controller, log, "BindDelayed(node-1,[a b])", controller.BindDelayed("node-1", []string{"a", "b"}), "整体指派为 a=pv-c,b=pv-b，按名单独贪心会让a抢占pv-b")
		if got := boundVolume(t, controller, "a"); got != "pv-c" {
			t.Fatalf("a got %q", got)
		}
		if got := boundVolume(t, controller, "b"); got != "pv-b" {
			t.Fatalf("b got %q", got)
		}
	})

	t.Run("minimum waste then lexicographic assignment", func(t *testing.T) {
		log := newOperationLog(t)
		controller := NewController()
		for _, spec := range []VolumeSpec{
			testVolume("pv-a", 2, "c"),
			testVolume("pv-b", 2, "c"),
			testVolume("pv-c", 3, "c"),
		} {
			requireOK(t, controller, log, "CreateVolume("+spec.Name+")", controller.CreateVolume(spec), "构造总浪费并列的指派")
		}
		for _, spec := range []ClaimSpec{testClaim("x", 1, "c", BindingDelayed), testClaim("y", 2, "c", BindingDelayed)} {
			requireOK(t, controller, log, "CreateClaim("+spec.Name+")", controller.CreateClaim(spec), "延迟声明保持待绑定")
		}
		requireOK(t, controller, log, "BindDelayed(node-1,[x y])", controller.BindDelayed("node-1", []string{"x", "y"}), "总浪费均为2时，先令声明x的卷名字典序最小")
		if got := boundVolume(t, controller, "x"); got != "pv-a" || boundVolume(t, controller, "y") != "pv-b" {
			t.Fatalf("got x=%s y=%s", got, boundVolume(t, controller, "y"))
		}
	})

	t.Run("node constraints and all-or-nothing", func(t *testing.T) {
		log := newOperationLog(t)
		controller := NewController()
		pvA := testVolume("pv-a", 1, "c")
		pvA.NodeNames = []string{"node-a"}
		pvB := testVolume("pv-b", 1, "c")
		pvB.NodeNames = []string{"node-b"}
		for _, spec := range []VolumeSpec{pvA, pvB} {
			requireOK(t, controller, log, "CreateVolume("+spec.Name+")", controller.CreateVolume(spec), "构造互斥节点约束")
		}
		for _, spec := range []ClaimSpec{testClaim("a", 1, "c", BindingDelayed), testClaim("b", 1, "c", BindingDelayed)} {
			requireOK(t, controller, log, "CreateClaim("+spec.Name+")", controller.CreateClaim(spec), "延迟声明保持待绑定")
		}
		requireCode(t, controller, log, "BindDelayed(node-a,[a b])", controller.BindDelayed("node-a", []string{"a", "b"}), ErrCodeNoMatchingVolume, "b无节点可用，整体拒绝且不改变任何状态")
		for _, name := range []string{"a", "b"} {
			if got := boundVolume(t, controller, name); got != "" {
				t.Fatalf("%s unexpectedly bound %s", name, got)
			}
		}
		pvAStatus, _ := controller.GetVolume("pv-a")
		pvBStatus, _ := controller.GetVolume("pv-b")
		if pvAStatus.State != VolumeAvailable || pvBStatus.State != VolumeAvailable {
			t.Fatalf("volumes changed: %s,%s", pvAStatus.State, pvBStatus.State)
		}
	})
}

func TestDelayedInvalidArguments(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	requireOK(t, controller, log, "CreateVolume(pv)", controller.CreateVolume(testVolume("pv", 1, "c")), "准备立即模式声明")
	requireOK(t, controller, log, "CreateClaim(immediate)", controller.CreateClaim(testClaim("immediate", 1, "c", BindingImmediate)), "立即模式声明已绑定")
	requireCode(t, controller, log, "BindDelayed(node,[immediate])", controller.BindDelayed("node", []string{"immediate"}), ErrCodeInvalidArgument, "非延迟模式出现在联合绑定中为参数非法")
	requireCode(t, controller, log, "BindDelayed(node,[missing])", controller.BindDelayed("node", []string{"missing"}), ErrCodeNotFound, "对象不存在优先于无匹配卷，但低于参数非法")
}
