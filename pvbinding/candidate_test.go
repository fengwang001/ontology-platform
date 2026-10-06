package pvbinding

import "testing"

func TestCandidateBoundariesAndImmediateSelection(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()

	volumes := []VolumeSpec{
		testVolume("small", 100, "other-fast"),
		testVolume("exact-a", 10, "fast"),
		testVolume("large", 1, "fast"),
		testVolume("other-class", 10, "slow"),
	}
	exactB := testVolume("exact-b", 10, "fast")
	exactB.AccessModes = []string{"RWX"}
	volumes = append(volumes[:2], append([]VolumeSpec{exactB}, volumes[2:]...)...)
	manyModes := testVolume("many-modes", 20, "fast")
	manyModes.AccessModes = []string{"RWO", "ROX"}
	volumes = append(volumes, manyModes)
	labeled := testVolume("labeled", 20, "fast")
	labeled.Labels = map[string]string{"zone": "a"}
	volumes = append(volumes, labeled)
	reserved := testVolume("reserved", 5, "fast")
	reserved.ReservationName = "target"
	reserved.HasReservation = true
	volumes = append(volumes, reserved)

	for _, spec := range volumes {
		requireOK(t, controller, log, "CreateVolume("+describe(spec)+")", controller.CreateVolume(spec), "可用卷加入同存储类索引")
	}

	requireOK(t, controller, log, "CreateClaim(capacity-tie)", controller.CreateClaim(testClaim("capacity-tie", 10, "fast", BindingImmediate)), "容量并列时名称取 exact-a")
	if got := boundVolume(t, controller, "capacity-tie"); got != "exact-a" {
		t.Fatalf("capacity tie selected %q", got)
	}

	modeClaim := testClaim("need-rox", 1, "fast", BindingImmediate)
	modeClaim.AccessModes = []string{"ROX"}
	requireOK(t, controller, log, "CreateClaim(need-rox)", controller.CreateClaim(modeClaim), "访问模式集合必须包含全部所需模式")
	if got := boundVolume(t, controller, "need-rox"); got != "many-modes" {
		t.Fatalf("mode match selected %q", got)
	}

	selectorClaim := testClaim("need-label", 1, "fast", BindingImmediate)
	selectorClaim.Selector = map[string]string{"zone": "a"}
	requireOK(t, controller, log, "CreateClaim(need-label)", controller.CreateClaim(selectorClaim), "标签选择器要求所有键值相等")
	if got := boundVolume(t, controller, "need-label"); got != "labeled" {
		t.Fatalf("selector match selected %q", got)
	}

	requireOK(t, controller, log, "CreateClaim(target)", controller.CreateClaim(testClaim("target", 5, "fast", BindingImmediate)), "预留卷只接受同名声明")
	if got := boundVolume(t, controller, "target"); got != "reserved" {
		t.Fatalf("reservation selected %q", got)
	}

	requireOK(t, controller, log, "CreateClaim(other-reservation)", controller.CreateClaim(testClaim("other-reservation", 50, "fast", BindingImmediate)), "无候选时待绑定，不返回错误")
	if got := boundVolume(t, controller, "other-reservation"); got != "" {
		t.Fatalf("pending claim bound to %q", got)
	}
}

func TestSpecifiedVolumeDoesNotSwitch(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	for _, spec := range []VolumeSpec{testVolume("good", 5, "fast"), testVolume("bad", 100, "slow")} {
		requireOK(t, controller, log, "CreateVolume("+spec.Name+")", controller.CreateVolume(spec), "创建指定卷和其他可匹配卷")
	}
	claim := testClaim("fixed", 10, "fast", BindingImmediate)
	claim.VolumeName = "bad"
	claim.HasVolumeName = true
	requireOK(t, controller, log, "CreateClaim(fixed->bad)", controller.CreateClaim(claim), "指定卷容量不足时必须等待 bad，不能改选 good")
	if got := boundVolume(t, controller, "fixed"); got != "" {
		t.Fatalf("specified volume fallback selected %q", got)
	}
	updated := testVolume("bad", 10, "fast")
	requireOK(t, controller, log, "UpdateVolume(bad)", controller.UpdateVolume(updated), "卷修改触发待绑定声明按名重评估")
	if got := boundVolume(t, controller, "fixed"); got != "bad" {
		t.Fatalf("updated specified volume selected %q", got)
	}
}
