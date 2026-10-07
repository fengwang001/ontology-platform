package ontology

import "testing"

type scope func(in Input) bool

func (s scope) Covers(in Input) bool { return s(in) }
func (s scope) Description() string  { return "amount <= 1000" }

// 替换生效前已开始执行的在途调用必须用旧逻辑执行到底；
// 替换生效后发起的调用必须用新逻辑。
func TestHotReplaceInFlightVsNewCalls(t *testing.T) {
	d, _ := mustSetup(t)

	started := make(chan struct{})
	proceed := make(chan struct{})

	old := Logic{
		ID: "old",
		Execute: func(_ *Object, in Input) (Output, error) {
			close(started)
			<-proceed
			return Output{"by": "old"}, nil
		},
	}
	new := logicID("new")

	if _, err := d.InstallLogic("pay", "Company", old, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}

	type res struct {
		out Output
		r   *DispatchRecord
	}
	oldCall := make(chan res, 1)
	go func() {
		out, r := d.Invoke("o", "pay", Input{"amount": 1})
		oldCall <- res{out, r}
	}()
	<-started

	// 在途调用挂起期间热替换。
	if _, err := d.InstallLogic("pay", "Company", new, nil); err != nil {
		t.Fatal(err)
	}

	// 替换后新发起的调用立即使用新逻辑（不等待在途调用）。
	out2, rec2 := d.Invoke("o", "pay", Input{"amount": 1})
	if rec2.LogicID != "new" || out2["by"] != "new" {
		t.Fatalf("new call must use new logic, got %s", rec2.LogicID)
	}

	close(proceed)
	r1 := <-oldCall
	if r1.r.LogicID != "old" || r1.out["by"] != "old" {
		t.Fatalf("in-flight call must finish with old logic, got %s", r1.r.LogicID)
	}

	// 两条记录应分别固定到不同逻辑与不同注册表版本。
	if r1.r.RegistryVer == rec2.RegistryVer {
		t.Fatalf("in-flight and new calls must pin different registry versions")
	}
}

// 审计：拒绝变放行必须声明放宽；未声明、超范围、范围内都要被正确识别。
func TestAuditRelaxation(t *testing.T) {
	d, _ := mustSetup(t)
	if err := d.CreateObject("probe", "Company"); err != nil {
		t.Fatal(err)
	}
	obj := d.objects["probe"]

	// 1) 未声明放宽：amount=500 旧拒绝、新放行 -> not-declared。
	if _, err := d.InstallLogic("pay", "Company", rejectingLogic("strict"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InstallLogic("pay", "Company", logicID("lenient"), nil); err != nil {
		t.Fatal(err)
	}
	findings := d.AuditType("pay", "Company",
		[]Input{{"amount": 500}, {"amount": 1}}, obj)
	if len(findings) != 1 || findings[0].Kind != FindingRelaxationNotDeclared {
		t.Fatalf("want 1 not-declared finding, got %+v", findings)
	}

	// 2) 重新装回严格旧逻辑，再以“只覆盖 <=100”的声明替换：
	// amount=500 在声明范围外 -> out-of-scope；amount=50 在范围内 -> 无发现。
	if _, err := d.InstallLogic("pay", "Company", rejectingLogic("strict2"), nil); err != nil {
		t.Fatal(err)
	}
	within := scope(func(in Input) bool { return in["amount"].(int) <= 100 })
	if _, err := d.InstallLogic("pay", "Company", logicID("lenient2"), within); err != nil {
		t.Fatal(err)
	}
	if got := d.RelaxDeclaredAt("pay", "Company"); got == nil {
		t.Fatal("relax metadata should be exposed")
	}
	findings = d.AuditType("pay", "Company",
		[]Input{{"amount": 500}, {"amount": 50}}, obj)
	if len(findings) != 1 || findings[0].Kind != FindingRelaxationOutOfScope {
		t.Fatalf("want 1 out-of-scope finding, got %+v", findings)
	}

	// 3) 严格旧逻辑 + 覆盖全部输入的声明 -> 审计无发现。
	if _, err := d.InstallLogic("pay", "Company", rejectingLogic("strict3"), nil); err != nil {
		t.Fatal(err)
	}
	all := scope(func(in Input) bool { return true })
	if _, err := d.InstallLogic("pay", "Company", logicID("lenient3"), all); err != nil {
		t.Fatal(err)
	}
	if findings = d.AuditType("pay", "Company",
		[]Input{{"amount": 500}, {"amount": 50}}, obj); len(findings) != 0 {
		t.Fatalf("fully covered relaxation must audit clean, got %+v", findings)
	}

	// 4) 注册不被阻止：哪怕未声明放宽，新逻辑依然生效。
	if _, err := d.InstallLogic("pay", "Company", logicID("lenient4"), nil); err != nil {
		t.Fatal(err)
	}
	out, rec := d.Invoke("probe", "pay", Input{"amount": 9999})
	if rec.Status != StatusOK || out["by"] != "lenient4" {
		t.Fatalf("registration must not be blocked, status=%s", rec.Status)
	}
}

// 新逻辑若悄悄收紧（放行变拒绝），也应作为其他差异被审计发现。
func TestAuditUndeclaredTightening(t *testing.T) {
	d, _ := mustSetup(t)
	if err := d.CreateObject("p", "Company"); err != nil {
		t.Fatal(err)
	}
	obj := d.objects["p"]
	if _, err := d.InstallLogic("pay", "Company", logicID("open"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InstallLogic("pay", "Company", rejectingLogic("tight"), scope(func(Input) bool { return false })); err != nil {
		t.Fatal(err)
	}
	findings := d.AuditType("pay", "Company", []Input{{"amount": 500}}, obj)
	if len(findings) != 1 || findings[0].Kind != FindingOtherDivergence {
		t.Fatalf("want other-divergence tightening finding, got %+v", findings)
	}
}
