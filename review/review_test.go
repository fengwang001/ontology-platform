package review

import (
	"reflect"
	"testing"

	"ontology/formulary"
	"ontology/interact"
)

func it(drug string, pills, times, s, e int) Item {
	return Item{Drug: drug, Pills: pills, Times: times, S: s, E: e}
}

// baseSetup 配置公共目录、相互作用、过敏与人员；T 固定为 3。
func baseSetup(t *testing.T, fm *formulary.Store, ix *interact.Store, e *Engine) {
	t.Helper()
	drugs := []struct {
		id, ing string
		mg      int64
		level   int
	}{
		{"P1", "X", 500, 1}, {"P2", "X", 650, 1}, {"P3", "X", 250, 1},
		{"P4", "X", 1001, 1}, {"PL", "X", 500, 2}, {"PH", "X", 500, 3},
		{"W1", "W", 100, 1}, {"S1", "S", 100, 1}, {"F1", "F", 100, 1},
		{"PY", "Y", 100, 1}, {"PZ", "Z", 100, 1},
		{"PA", "A", 100, 1}, {"PB", "B", 100, 1}, {"PC", "C", 100, 1},
	}
	for _, d := range drugs {
		if err := fm.AddDrug(d.id, d.ing, d.mg, d.level); err != nil {
			t.Fatalf("AddDrug %s: %v", d.id, err)
		}
	}
	if err := fm.SetMax("X", 4000); err != nil {
		t.Fatalf("SetMax: %v", err)
	}
	pairs := []struct {
		a, b  string
		grade int
	}{
		{"W", "S", 2}, {"W", "F", 3}, {"S", "F", 1},
		{"X", "Y", 2}, {"X", "Z", 3},
		{"A", "C", 2}, {"A", "B", 1}, {"B", "C", 1},
	}
	for _, p := range pairs {
		if err := ix.SetPair(p.a, p.b, p.grade); err != nil {
			t.Fatalf("SetPair %s-%s: %v", p.a, p.b, err)
		}
	}
	if err := ix.SetAllergy("p1", "Z"); err != nil {
		t.Fatalf("SetAllergy: %v", err)
	}
	for id, lv := range map[string]int{"d1": 1, "d2": 2, "d3": 3} {
		if c := e.AddDoctor(id, lv); c != OK {
			t.Fatalf("AddDoctor %s: %v", id, c)
		}
	}
	if c := e.AddPharmacist("ph"); c != OK {
		t.Fatalf("AddPharmacist: %v", c)
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		if c := e.AddPatient(id); c != OK {
			t.Fatalf("AddPatient %s: %v", id, c)
		}
	}
}

type step struct {
	op      string // submit / approve / deny / stop
	now     int
	who     string // 医生或药师
	patient string
	items   []Item
	rx      int
	// Submit 期望
	wantCode   Code
	wantIndex  int
	wantRxID   int
	wantStatus Status
	wantWarns  []Warning
}

func sub(now int, who, patient string, items []Item, code Code, index, rxID int, st Status, warns []Warning) step {
	return step{op: "submit", now: now, who: who, patient: patient, items: items,
		wantCode: code, wantIndex: index, wantRxID: rxID, wantStatus: st, wantWarns: warns}
}

func adjud(op string, now int, who string, rx int, code Code) step {
	return step{op: op, now: now, who: who, rx: rx, wantCode: code}
}

func runSteps(t *testing.T, steps []step) {
	t.Helper()
	fm := formulary.NewStore()
	ix := interact.NewStore()
	e, err := NewEngine(fm, ix, 3)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	baseSetup(t, fm, ix, e)
	for i, st := range steps {
		switch st.op {
		case "submit":
			got := e.Submit(st.now, st.who, st.patient, st.items)
			if got.Code != st.wantCode || got.Index != st.wantIndex ||
				got.RxID != st.wantRxID || got.Status != st.wantStatus {
				t.Errorf("step %d submit: got {code=%v index=%d rx=%d status=%v}, want {code=%v index=%d rx=%d status=%v}",
					i, got.Code, got.Index, got.RxID, got.Status,
					st.wantCode, st.wantIndex, st.wantRxID, st.wantStatus)
			}
			if !warnsEqual(got.Warnings, st.wantWarns) {
				t.Errorf("step %d submit: warns got %v, want %v", i, got.Warnings, st.wantWarns)
			}
		default:
			var got Code
			switch st.op {
			case "approve":
				got = e.Approve(st.now, st.who, st.rx)
			case "deny":
				got = e.Deny(st.now, st.who, st.rx)
			case "stop":
				got = e.Stop(st.now, st.who, st.rx)
			}
			if got != st.wantCode {
				t.Errorf("step %d %s: got %v, want %v", i, st.op, got, st.wantCode)
			}
		}
	}
}

func warnsEqual(a, b []Warning) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// 题面例 1：日极量恰等、多 1、首尾相接不重叠。
func TestDailyLimitBoundary(t *testing.T) {
	runSteps(t, []step{
		// P1 日剂量 3000 于 [1,8)
		sub(1, "d3", "p1", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 0, StatusActive, nil),
		// 第 7 日合计 3000+1300=4300 > 4000
		sub(1, "d3", "p1", []Item{it("P2", 1, 2, 7, 10)}, ErrOverdose, -1, -1, StatusNone, nil),
		// [8,10) 与 [1,8) 首尾相接，无公共日，通过
		sub(1, "d3", "p1", []Item{it("P2", 1, 2, 8, 10)}, OK, -1, 1, StatusActive, nil),
		// 每日 1 次 650：第 7 日合计 3650，通过
		sub(1, "d3", "p1", []Item{it("P2", 1, 1, 7, 10)}, OK, -1, 2, StatusActive, nil),
		// 换患者：3000+1000=4000 恰等，通过
		sub(1, "d3", "p2", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 3, StatusActive, nil),
		sub(1, "d3", "p2", []Item{it("P3", 2, 2, 7, 10)}, OK, -1, 4, StatusActive, nil),
		// 换患者：3000+1001=4001 多 1，拒绝
		sub(1, "d3", "p3", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 5, StatusActive, nil),
		sub(1, "d3", "p3", []Item{it("P4", 1, 1, 7, 9)}, ErrOverdose, -1, -1, StatusNone, nil),
	})
}

// 题面例 2：禁忌报下标、待审与提示清单、到期前可获批。
func TestInteractionExample(t *testing.T) {
	runSteps(t, []step{
		// 患者在用含 W 的药于 [1,30)
		sub(1, "d3", "p1", []Item{it("W1", 1, 1, 1, 30)}, OK, -1, 0, StatusActive, nil),
		// [S,F] 于 [5,10)：F 与 W 禁忌，报下标 1
		sub(1, "d3", "p1", []Item{it("S1", 1, 1, 5, 10), it("F1", 1, 1, 5, 10)},
			ErrContra, 1, -1, StatusNone, nil),
		// 只提交 S：与 W 等级 2，待审，清单 (S,W)
		sub(1, "d3", "p1", []Item{it("S1", 1, 1, 5, 10)},
			OK, -1, 1, StatusPending, []Warning{{A: "S", B: "W", Grade: 2}}),
		// 再提交 F 于 [6,8)：与 W 禁忌，被拒
		sub(1, "d3", "p1", []Item{it("F1", 1, 1, 6, 8)}, ErrContra, 0, -1, StatusNone, nil),
		// T=3，提交日 1：now=3 仍可 Approve
		adjud("approve", 3, "ph", 1, OK),
		// 已生效，重复 Approve 状态不符
		adjud("approve", 3, "ph", 1, ErrState),
	})
}

// 待审到期取等：now = 提交日+T 即作废。
func TestPendingExpiryBoundary(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d3", "p1", []Item{it("W1", 1, 1, 1, 30)}, OK, -1, 0, StatusActive, nil),
		sub(1, "d3", "p1", []Item{it("S1", 1, 1, 5, 10)},
			OK, -1, 1, StatusPending, []Warning{{A: "S", B: "W", Grade: 2}}),
		// now=4 = 1+T：操作开头即已作废，Approve/Deny 均状态不符
		adjud("approve", 4, "ph", 1, ErrState),
		adjud("deny", 4, "ph", 1, ErrState),
		// 到期作废已释放：同项可再次提交（与 W 仍等级 2，待审）
		sub(5, "d3", "p1", []Item{it("S1", 1, 1, 5, 10)},
			OK, -1, 2, StatusPending, []Warning{{A: "S", B: "W", Grade: 2}}),
	})
}

// Stop 截断后释放：区间变为 [1,6)，其后不再重叠；清单只剩 (F,S) 等级 1。
func TestStopTruncatesAndReleases(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d3", "p1", []Item{it("W1", 1, 1, 1, 30)}, OK, -1, 0, StatusActive, nil),
		// F 于 [6,8) 与 W 禁忌
		sub(1, "d3", "p1", []Item{it("F1", 1, 1, 6, 8)}, ErrContra, 0, -1, StatusNone, nil),
		// S 于 [1,30)：与 W 等级 2，待审后获批生效
		sub(1, "d3", "p1", []Item{it("S1", 1, 1, 1, 30)},
			OK, -1, 1, StatusPending, []Warning{{A: "S", B: "W", Grade: 2}}),
		adjud("approve", 1, "ph", 1, OK),
		// 非原开方医生且非等级 3：无权限；不存在医生：不存在
		adjud("stop", 6, "d2", 0, ErrPermission),
		adjud("stop", 6, "dx", 0, ErrNotFound),
		// 第 6 日 Stop 含 W 处方（等级 3 医生），区间变 [1,6)
		adjud("stop", 6, "d3", 0, OK),
		// 再提交 F 于 [6,8)：与 W 不再重叠；与 S 等级 1，生效，清单 (F,S)
		sub(6, "d3", "p1", []Item{it("F1", 1, 1, 6, 8)},
			OK, -1, 2, StatusActive, []Warning{{A: "F", B: "S", Grade: 1}}),
	})
}

// 待审占额：待审项参与日极量；Deny 后释放。
func TestPendingOccupiesQuota(t *testing.T) {
	runSteps(t, []step{
		// X 日剂量 3000，与 Y 等级 2 → 待审
		sub(1, "d3", "p1", []Item{it("P1", 2, 3, 1, 8), it("PY", 1, 1, 1, 8)},
			OK, -1, 0, StatusPending, []Warning{{A: "X", B: "Y", Grade: 2}}),
		// 再 3000：与待审合计 6000 > 4000（待审照常占额）
		sub(1, "d3", "p1", []Item{it("P1", 2, 3, 1, 8)}, ErrOverdose, -1, -1, StatusNone, nil),
		// Deny 释放占额
		adjud("deny", 1, "ph", 0, OK),
		sub(1, "d3", "p1", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 1, StatusActive, nil),
	})
}

// 禁忌先于超日极量。
func TestContraBeforeOverdose(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d3", "p2", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 0, StatusActive, nil),
		// PZ(Z) 与 X 禁忌，且新项 X 3000 与参照合计 6000 超量：报禁忌
		sub(1, "d3", "p2", []Item{it("PZ", 1, 1, 1, 8), it("P1", 2, 3, 1, 8)},
			ErrContra, 0, -1, StatusNone, nil),
	})
}

// 过敏先于禁忌。
func TestAllergyBeforeContra(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d3", "p1", []Item{it("P1", 2, 3, 1, 8)}, OK, -1, 0, StatusActive, nil),
		// p1 对 Z 过敏；Z 与 X 亦禁忌：报过敏
		sub(1, "d3", "p1", []Item{it("PZ", 1, 1, 1, 8)}, ErrAllergy, 0, -1, StatusNone, nil),
	})
}

// 无处方权报下标最小项；参数非法各形态；不存在三类。
func TestPermissionAndParamOrder(t *testing.T) {
	runSteps(t, []step{
		// d1 等级 1：P1 等级 1 通过，PH 等级 3 越权，报下标 1
		sub(1, "d1", "p3", []Item{it("P1", 1, 1, 1, 8), it("PH", 1, 1, 1, 8)},
			ErrPermission, 1, -1, StatusNone, nil),
		sub(1, "d3", "p3", []Item{it("PH", 1, 1, 1, 8)}, OK, -1, 0, StatusActive, nil),
		// 参数非法：同药两项 / 片数 0 / now>s / s==e / 次数>12
		sub(1, "d3", "p3", []Item{it("P1", 1, 1, 2, 8), it("P1", 1, 1, 3, 9)},
			ErrParam, -1, -1, StatusNone, nil),
		sub(1, "d3", "p3", []Item{it("P1", 0, 1, 1, 8)}, ErrParam, -1, -1, StatusNone, nil),
		sub(5, "d3", "p3", []Item{it("P1", 1, 1, 3, 8)}, ErrParam, -1, -1, StatusNone, nil),
		sub(1, "d3", "p3", []Item{it("P1", 1, 1, 8, 8)}, ErrParam, -1, -1, StatusNone, nil),
		sub(1, "d3", "p3", []Item{it("P1", 1, 13, 1, 8)}, ErrParam, -1, -1, StatusNone, nil),
		// 医生 / 患者 / 药品不存在
		sub(1, "d9", "p3", []Item{it("P1", 1, 1, 1, 8)}, ErrNotFound, -1, -1, StatusNone, nil),
		sub(1, "d3", "p9", []Item{it("P1", 1, 1, 1, 8)}, ErrNotFound, -1, -1, StatusNone, nil),
		sub(1, "d3", "p3", []Item{it("NOPE", 1, 1, 1, 8)}, ErrNotFound, -1, -1, StatusNone, nil),
	})
}

// 提示清单排序：等级降序，再按（小成分，大成分）升序。
func TestWarningOrdering(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d3", "p3", []Item{it("PA", 1, 1, 1, 8), it("PB", 1, 1, 1, 8), it("PC", 1, 1, 1, 8)},
			OK, -1, 0, StatusPending, []Warning{
				{A: "A", B: "C", Grade: 2},
				{A: "A", B: "B", Grade: 1},
				{A: "B", B: "C", Grade: 1},
			}),
	})
}

// 被拒不落地：不推进时钟、不消耗处方号；时钟回退拒绝。
func TestRejectedOpNotLanded(t *testing.T) {
	runSteps(t, []step{
		sub(5, "d3", "p1", []Item{it("P1", 2, 3, 5, 10)}, OK, -1, 0, StatusActive, nil),
		// now=9 超量被拒：时钟仍停在 5，处方号未消耗
		sub(9, "d3", "p1", []Item{it("P1", 2, 3, 9, 12)}, ErrOverdose, -1, -1, StatusNone, nil),
		// now=7（小于被拒的 9）仍被接受，且处方号为 1
		sub(7, "d3", "p1", []Item{it("P1", 1, 1, 7, 8)}, OK, -1, 1, StatusActive, nil),
		// now=6 < 7：时钟回退
		sub(6, "d3", "p1", []Item{it("P1", 1, 1, 8, 9)}, ErrClock, -1, -1, StatusNone, nil),
	})
}

// Approve/Deny/Stop 的存在性、参数与状态检查。
func TestAdjudicationChecks(t *testing.T) {
	runSteps(t, []step{
		sub(1, "d2", "p1", []Item{it("W1", 1, 1, 1, 30)}, OK, -1, 0, StatusActive, nil),
		// 生效处方 Approve：状态不符；药师不存在 / 处方不存在 / 处方号非法
		adjud("approve", 2, "ph", 0, ErrState),
		adjud("approve", 2, "d2", 0, ErrNotFound),
		adjud("deny", 2, "ph", 99, ErrNotFound),
		adjud("approve", 2, "ph", -1, ErrParam),
		// 原开方医生 Stop 成功；其后 Deny 状态不符
		adjud("stop", 2, "d2", 0, OK),
		adjud("deny", 3, "ph", 0, ErrState),
	})
}

// 首次被接受的 Submit 之后，全部配置接口报已冻结。
func TestConfigFrozenAfterFirstSubmit(t *testing.T) {
	fm := formulary.NewStore()
	ix := interact.NewStore()
	e, err := NewEngine(fm, ix, 3)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	baseSetup(t, fm, ix, e)
	if got := e.Submit(1, "d3", "p1", []Item{it("P1", 1, 1, 1, 8)}); got.Code != OK {
		t.Fatalf("submit: %v", got.Code)
	}
	if err := fm.AddDrug("PX", "Q", 10, 1); err != formulary.ErrFrozen {
		t.Errorf("AddDrug after freeze: %v", err)
	}
	if err := fm.SetMax("Q", 10); err != formulary.ErrFrozen {
		t.Errorf("SetMax after freeze: %v", err)
	}
	if err := ix.SetPair("Q", "R", 1); err != interact.ErrFrozen {
		t.Errorf("SetPair after freeze: %v", err)
	}
	if err := ix.SetAllergy("p2", "Q"); err != interact.ErrFrozen {
		t.Errorf("SetAllergy after freeze: %v", err)
	}
	if c := e.AddDoctor("d9", 1); c != ErrFrozen {
		t.Errorf("AddDoctor after freeze: %v", c)
	}
	if c := e.AddPharmacist("ph2"); c != ErrFrozen {
		t.Errorf("AddPharmacist after freeze: %v", c)
	}
	if c := e.AddPatient("p9"); c != ErrFrozen {
		t.Errorf("AddPatient after freeze: %v", c)
	}
}
