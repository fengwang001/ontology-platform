package federation

import "testing"

// 1. 取整余量的并列打破：小数相同 -> 当前承载多者优先；再相同 -> 名称升序。
func TestRemainderTieBreak(t *testing.T) {
	l := newTestLog(t, "tiebreak")
	defer l.close()
	in := []naiveInput{
		{name: "a", weight: 2, min: 0, max: -1, capacity: 100, available: true, current: 1},
		{name: "b", weight: 2, min: 0, max: -1, capacity: 100, available: true, current: 5},
	}
	logInput(l, "tie-current", in, 1)
	r := regFromNaive(t, in)
	res := mustAlloc(t, r, 1)
	logResult(l, "tie-current", res, nil)
	if res.Targets["a"].Int64() != 0 || res.Targets["b"].Int64() != 1 {
		t.Fatalf("判定依据: 小数并列时 current 更大的 b 补 1，得到 a=%s b=%s",
			res.Targets["a"], res.Targets["b"])
	}
	l.printf("判定: PASS，小数并列由 current 打破")

	in2 := []naiveInput{
		{name: "b", weight: 3, min: 0, max: -1, capacity: 100, available: true, current: 2},
		{name: "a", weight: 3, min: 0, max: -1, capacity: 100, available: true, current: 2},
	}
	logInput(l, "tie-name", in2, 1)
	res2 := mustAlloc(t, regFromNaive(t, in2), 1)
	logResult(l, "tie-name", res2, nil)
	if res2.Targets["a"].Int64() != 1 || res2.Targets["b"].Int64() != 0 {
		t.Fatalf("判定依据: 全并列时名称升序 a 优先，得到 a=%s b=%s",
			res2.Targets["a"], res2.Targets["b"])
	}
	l.printf("判定: PASS，current 再并列时由名称升序打破")
}

// 2. 多轮饱和后的再分配。
func TestMultiRoundSaturation(t *testing.T) {
	l := newTestLog(t, "multiround")
	defer l.close()
	in := []naiveInput{
		{name: "a", weight: 10, min: 0, max: -1, capacity: 11, available: true},
		{name: "b", weight: 1, min: 0, max: -1, capacity: 2, available: true},
		{name: "c", weight: 1, min: 0, max: -1, capacity: 20, available: true},
	}
	logInput(l, "multi", in, 15)
	res := mustAlloc(t, regFromNaive(t, in), 15)
	logResult(l, "multi", res, nil)
	want := map[string]int64{"a": 11, "b": 2, "c": 2}
	got := toInt64Map(t, res)
	for n, v := range want {
		if got[n] != v {
			t.Fatalf("判定依据: 期望 %s=%d，实际 %d", n, v, got[n])
		}
	}
	if sumTargetsInt64(got) != 15 {
		t.Fatalf("目标之和必须恰好等于 15，得到 %d", sumTargetsInt64(got))
	}
	l.printf("判定: PASS，a/b 先后饱和，超出份额逐轮重分给 c，总和恰为 15")
}

// 3. 零权重集群只获得最小副本数。
func TestZeroWeightCluster(t *testing.T) {
	l := newTestLog(t, "zeroweight")
	defer l.close()
	in := []naiveInput{
		{name: "z", weight: 0, min: 3, max: -1, capacity: 10, available: true},
		{name: "w", weight: 1, min: 0, max: -1, capacity: 100, available: true},
	}
	logInput(l, "zero", in, 10)
	res := mustAlloc(t, regFromNaive(t, in), 10)
	logResult(l, "zero", res, nil)
	if res.Targets["z"].Int64() != 3 || res.Targets["w"].Int64() != 7 {
		t.Fatalf("判定依据: 零权重集群只得 min=3，w 得 7；得到 z=%s w=%s",
			res.Targets["z"], res.Targets["w"])
	}
	l.printf("判定: PASS，零权重可用集群只获得最小副本数")
}

// 4. 不可用集群与已删除集群的迁出。
func TestUnavailableEviction(t *testing.T) {
	l := newTestLog(t, "unavail")
	defer l.close()
	in := []naiveInput{
		{name: "down", weight: 5, min: 2, max: -1, capacity: 50, available: false, current: 8},
		{name: "up", weight: 1, min: 0, max: -1, capacity: 100, available: true, current: 2},
	}
	logInput(l, "down", in, 10)
	res := mustAlloc(t, regFromNaive(t, in), 10)
	logResult(l, "down", res, nil)
	if res.Targets["down"].Int64() != 0 || res.Targets["up"].Int64() != 10 {
		t.Fatalf("判定依据: 不可用集群目标恒为 0，10 全给 up")
	}
	if res.Migration.Int64() != 8 {
		t.Fatalf("判定依据: down 迁出 8 => migration=8，实际 %s", res.Migration)
	}
	changed := map[string]bool{}
	for _, ch := range res.Changes {
		changed[ch.Name] = true
	}
	if !changed["down"] {
		t.Fatalf("不可用集群迁出必须出现在变更计划中")
	}
	l.printf("判定: PASS，down 目标 0 且迁出 8 计入迁移量")

	inDead := []naiveInput{
		{name: "gone", weight: 1, min: 0, max: -1, capacity: 10, available: true, current: 4, dead: true},
		{name: "stay", weight: 1, min: 0, max: -1, capacity: 100, available: true},
	}
	logInput(l, "dead", inDead, 6)
	resd := mustAlloc(t, regFromNaive(t, inDead), 6)
	logResult(l, "dead", resd, nil)
	if resd.Targets["gone"].Int64() != 0 || resd.Migration.Int64() != 4 {
		t.Fatalf("判定依据: 已删除集群目标 0、迁出 4，得到 %v migration=%s",
			resd.Targets, resd.Migration)
	}
	l.printf("判定: PASS，删除集群承载在下次分配中迁出")
}

// 5. 最小副本冲突。
func TestMinConfigConflict(t *testing.T) {
	l := newTestLog(t, "conflict")
	defer l.close()
	in := []naiveInput{
		{name: "ok", weight: 1, min: 0, max: -1, capacity: 100, available: true},
		{name: "bad", weight: 1, min: 5, max: 10, capacity: 4, available: true},
	}
	logInput(l, "conflict-max", in, 10)
	r := regFromNaive(t, in)
	_, err := r.Allocate(bigI(10))
	logResult(l, "conflict-max", nil, err)
	if err == nil || codeOf(err) != ErrConfigConflict {
		t.Fatalf("判定依据: min(5)>有效上限(min(max=10,cap=4)=4) 必须报配置冲突，err=%v", err)
	}

	in2 := []naiveInput{
		{name: "bad2", weight: 1, min: 7, max: 20, capacity: 6, available: true},
	}
	logInput(l, "conflict-cap", in2, 10)
	r2 := regFromNaive(t, in2)
	_, err2 := r2.Allocate(bigI(10))
	logResult(l, "conflict-cap", nil, err2)
	if err2 == nil || codeOf(err2) != ErrConfigConflict {
		t.Fatalf("判定依据: min(7)>cap(6) 必须报配置冲突，err=%v", err2)
	}
	// 拒绝不得改变登记状态。
	if got := r2.live["bad2"]; got == nil || got.Min.Int64() != 7 {
		t.Fatalf("被拒绝请求后登记状态被意外修改")
	}
	l.printf("判定: PASS，min>有效上限 精确识别为配置冲突且不修改状态")
}

// 6. 容量不足的缺口。
func TestInsufficientCapacity(t *testing.T) {
	l := newTestLog(t, "capacity")
	defer l.close()
	in := []naiveInput{
		{name: "a", weight: 1, min: 0, max: 3, capacity: 3, available: true},
		{name: "b", weight: 2, min: 0, max: 4, capacity: 4, available: true},
	}
	logInput(l, "cap", in, 10)
	_, err := regFromNaive(t, in).Allocate(bigI(10))
	logResult(l, "cap", nil, err)
	if err == nil || codeOf(err) != ErrInsufficientCapacity {
		t.Fatalf("判定依据: 总有效上限 7 < total 10 必须报容量不足，err=%v", err)
	}
	if msg := err.Error(); !contains(msg, "3") {
		t.Fatalf("判定依据: 错误必须报告缺口大小 3，msg=%s", msg)
	}
	// 最小保障本身就占满全部容量且仍欠副本 => 缺口为 total-sumMin。
	in2 := []naiveInput{
		{name: "x", weight: 0, min: 4, max: 4, capacity: 4, available: true},
	}
	logInput(l, "cap-min", in2, 9)
	_, err2 := regFromNaive(t, in2).Allocate(bigI(9))
	logResult(l, "cap-min", nil, err2)
	if err2 == nil || codeOf(err2) != ErrInsufficientCapacity || !contains(err2.Error(), "5") {
		t.Fatalf("判定依据: 零权重集群吃满容量后缺口 5，err=%v", err2)
	}
	l.printf("判定: PASS，容量不足被拒绝并报告精确缺口")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
