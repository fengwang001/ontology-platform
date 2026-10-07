package ontology

import "testing"

// 可验证的复杂度证明：
//
// resolveOnChain 的遍历长度只取决于“具体类型到命中祖先的实际深度”，
// 与系统中其它注册了该动作的、互不相关的类型数量无关。本测试通过直接
// 检查 DispatchRecord.Steps（链上实际检查层数），在无关注册规模增长 100 倍
// 前后断言步数恒定，给出机器可重复的证据。
func TestLookupCostIndependentOfUnrelatedRegistrations(t *testing.T) {
	d, _ := mustSetup(t)

	// 命中固定在深度 2 的 Company；Startup(0) -> Company(1) -> Entity(2)。
	if _, err := d.InstallLogic("pay", "Company", logicID("L-Company"), nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}

	measure := func() int {
		_, rec := d.Invoke("o", "pay", Input{"amount": 1})
		if rec.Status != StatusOK {
			t.Fatalf("status %s", rec.Status)
		}
		return rec.Steps
	}

	baseline := measure()
	if baseline != 2 { // 检查 Startup（未注册）后命中 Company
		t.Fatalf("baseline steps = %d, want 2 (actual depth to hit)", baseline)
	}

	// 引入大量互不相关类型并都注册 pay，不得增加本次查找步数。
	for i := 0; i < 1000; i++ {
		name := "Unrelated" + itoa(i)
		if err := d.DefineType(name, "Entity"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.InstallLogic("pay", name, logicID("X-"+name), nil); err != nil {
			t.Fatal(err)
		}
	}

	after := measure()
	if after != baseline {
		t.Fatalf("lookup steps grew from %d to %d as unrelated registrations grew; "+
			"must be bounded by chain depth only", baseline, after)
	}

	// 直接命中深度 0 时步数必须为 1；waive 跳到 Company 时步数为 2。
	if _, err := d.InstallLogic("pay", "Startup", logicID("L-Startup"), nil); err != nil {
		t.Fatal(err)
	}
	if direct := measure(); direct != 1 {
		t.Fatalf("direct hit steps = %d, want 1", direct)
	}
	if _, err := d.Waive("pay", "Startup"); err != nil {
		t.Fatal(err)
	}
	if waived := measure(); waived != 2 {
		t.Fatalf("waive-to-Company steps = %d, want 2", waived)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// 每次调用都记录分派路径、命中依据与最终结果，供事后核对。
func TestDispatchRecordForAuditTrail(t *testing.T) {
	d, _ := mustSetup(t)
	if _, err := d.InstallLogic("pay", "Company", logicID("L-Company"), nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}
	_, rec := d.Invoke("o", "pay", Input{"amount": 7})

	records := d.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	saved := records[0]
	if saved.Seq != rec.Seq || saved.LogicID != "L-Company" ||
		saved.HitBasis != HitInherited || saved.HitType != "Company" {
		t.Fatalf("record mismatch: %+v", saved)
	}
	wantPath := []string{"Startup", "Company"} // 实际遍历到命中即止，不浪费额外步数
	if len(saved.Path) != len(wantPath) {
		t.Fatalf("path = %v", saved.Path)
	}
	for i := range wantPath {
		if saved.Path[i] != wantPath[i] {
			t.Fatalf("path[%d]=%s want %s", i, saved.Path[i], wantPath[i])
		}
	}
	if saved.RegistryVer <= 0 || saved.ConcreteType != "Startup" ||
		saved.Output["by"] != "L-Company" {
		t.Fatalf("record missing audit fields: %+v", saved)
	}

	// Records 返回副本：调用方修改不影响内部状态。
	records[0].LogicID = "tampered"
	if d.Records()[0].LogicID != "L-Company" {
		t.Fatal("internal records must not be mutable via returned slice")
	}
}
