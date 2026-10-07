package ontology

import (
	"errors"
	"testing"
)

func mustSetup(t *testing.T) (*Dispatcher, Action) {
	t.Helper()
	d := NewDispatcher()
	act := Action{Name: "pay", Validate: func(in Input) error {
		if in["amount"] == nil {
			return errors.New("amount required")
		}
		return nil
	}}
	if err := d.DeclareAction(act); err != nil {
		t.Fatalf("declare: %v", err)
	}
	for _, def := range [][2]string{
		{"Entity", ""},
		{"Company", "Entity"},
		{"Startup", "Company"},
	} {
		if err := d.DefineType(def[0], def[1]); err != nil {
			t.Fatalf("define %s: %v", def[0], err)
		}
	}
	return d, act
}

func logicID(id string) Logic {
	return Logic{
		ID: id,
		Execute: func(_ *Object, in Input) (Output, error) {
			return Output{"by": id, "amount": in["amount"]}, nil
		},
	}
}

func rejectingLogic(id string) Logic {
	l := logicID(id)
	l.Pre = func(_ *Object, in Input) error {
		if in["amount"].(int) > 100 {
			return errors.New("amount too large")
		}
		return nil
	}
	return l
}

// 全部组合：直接注册 / 间接继承命中 / 显式放弃。
//
//	Company 注册 pay，Startup 三种状态：无注册、直接注册、显式放弃；
//	另测 Entity 也未注册时的两种“无法分派”，以及放弃后落到更上层祖先。
func TestDispatchPaths(t *testing.T) {
	cases := []struct {
		name      string
		startup   string // "", "direct", "waive"
		company   string // "", "direct"
		entity    string // "", "direct"
		objType   string
		wantStat  DispatchStatus
		wantBasis HitBasis
		wantType  string
	}{
		{"direct wins over inherited", "direct", "direct", "", "Startup", StatusOK, HitDirect, "Startup"},
		{"inherited when no direct", "", "direct", "", "Startup", StatusOK, HitInherited, "Company"},
		{"waive skips to ancestor", "waive", "direct", "", "Startup", StatusOK, HitInherited, "Company"},
		{"waive skips two levels", "waive", "waive", "direct", "Startup", StatusOK, HitInherited, "Entity"},
		{"none registered anywhere", "", "", "", "Startup", StatusNoDispatchNone, HitNone, ""},
		{"waived but no ancestor logic", "waive", "", "", "Startup", StatusNoDispatchWaived, HitNone, ""},
		{"waived company no entity logic", "waive", "waive", "", "Startup", StatusNoDispatchWaived, HitNone, ""},
		{"concrete Entity direct", "", "", "direct", "Entity", StatusOK, HitDirect, "Entity"},
		{"concrete Company inherited", "", "", "direct", "Company", StatusOK, HitInherited, "Entity"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := mustSetup(t)
			if tc.entity == "direct" {
				if _, err := d.InstallLogic("pay", "Entity", logicID("L-Entity"), nil); err != nil {
					t.Fatal(err)
				}
			}
			if tc.company == "direct" {
				if _, err := d.InstallLogic("pay", "Company", logicID("L-Company"), nil); err != nil {
					t.Fatal(err)
				}
			}
			if tc.company == "waive" {
				if _, err := d.Waive("pay", "Company"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.startup == "direct" {
				if _, err := d.InstallLogic("pay", "Startup", logicID("L-Startup"), nil); err != nil {
					t.Fatal(err)
				}
			}
			if tc.startup == "waive" {
				if _, err := d.Waive("pay", "Startup"); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.CreateObject("o1", tc.objType); err != nil {
				t.Fatal(err)
			}
			out, rec := d.Invoke("o1", "pay", Input{"amount": 5})
			if rec.Status != tc.wantStat {
				t.Fatalf("status = %q, want %q (err=%s)", rec.Status, tc.wantStat, rec.Err)
			}
			if rec.HitBasis != tc.wantBasis {
				t.Fatalf("basis = %q, want %q", rec.HitBasis, tc.wantBasis)
			}
			if rec.HitType != tc.wantType {
				t.Fatalf("hitType = %q, want %q", rec.HitType, tc.wantType)
			}
			if tc.wantStat == StatusOK {
				if out["by"] != "L-"+tc.wantType {
					t.Fatalf("output by = %v, want L-%s", out["by"], tc.wantType)
				}
			}
		})
	}
}

// 显式放弃与“从未注册”必须可区分：即使都无法分派，状态码与路径记录不同。
func TestWaiveDistinguishableFromAbsent(t *testing.T) {
	d, _ := mustSetup(t)
	if err := d.CreateObject("a", "Startup"); err != nil {
		t.Fatal(err)
	}
	_, absent := d.Invoke("a", "pay", Input{"amount": 1})
	if _, err := d.Waive("pay", "Startup"); err != nil {
		t.Fatal(err)
	}
	_, waived := d.Invoke("a", "pay", Input{"amount": 1})
	if absent.Status != StatusNoDispatchNone || waived.Status != StatusNoDispatchWaived {
		t.Fatalf("absent=%s waived=%s", absent.Status, waived.Status)
	}
	if len(waived.Path) != 3 || len(absent.Path) != 3 {
		t.Fatalf("path must traverse whole chain, got %v %v", absent.Path, waived.Path)
	}
}

// 无法分派必须在执行任何逻辑之前判定，且优先于撤销。
func TestNoDispatchBeforeRevoke(t *testing.T) {
	d, _ := mustSetup(t)
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeObject("o"); err != nil {
		t.Fatal(err)
	}
	_, rec := d.Invoke("o", "pay", Input{"amount": 1})
	if rec.Status != StatusNoDispatchNone {
		t.Fatalf("no-dispatch must be decided before revoke, got %s", rec.Status)
	}

	if _, err := d.Waive("pay", "Startup"); err != nil {
		t.Fatal(err)
	}
	_, rec2 := d.Invoke("o", "pay", Input{"amount": 1})
	if rec2.Status != StatusNoDispatchWaived {
		t.Fatalf("waived no-dispatch must precede revoke, got %s", rec2.Status)
	}
}

// 查找段（全序上）已撤销：整体失败，绝不执行写入。
func TestRevokedDuringLookupFailsWholeCall(t *testing.T) {
	d, _ := mustSetup(t)
	executed := false
	l := logicID("L-Company")
	l.Execute = func(o *Object, in Input) (Output, error) {
		executed = true
		return Output{"by": "L-Company"}, nil
	}
	if _, err := d.InstallLogic("pay", "Company", l, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeObject("o"); err != nil {
		t.Fatal(err)
	}
	out, rec := d.Invoke("o", "pay", Input{"amount": 1})
	if rec.Status != StatusRevokedDuringLookup {
		t.Fatalf("got %s: %s", rec.Status, rec.Err)
	}
	if out != nil || executed {
		t.Fatalf("logic must not run after lookup-time revoke")
	}
	if rec.HitType != "Company" {
		t.Fatalf("record should retain resolved hit type, got %q", rec.HitType)
	}
}

// 撤销发生在查找完成、逻辑开始运行之后：分派机制不干预，
// 是否完成由逻辑自身的后置校验决定。
func TestRevokeDuringExecutionLeftToPostcheck(t *testing.T) {
	d, _ := mustSetup(t)
	l := logicID("L-Company")
	l.Post = func(o *Object, _ Input, _ Output) error {
		if o.Revoked() {
			return errors.New("reject commit: object revoked")
		}
		return nil
	}
	block := make(chan struct{})
	release := make(chan struct{})
	l.Execute = func(o *Object, in Input) (Output, error) {
		close(block) // 通知测试：已进入执行
		<-release    // 等待测试先撤销
		return Output{"by": "L-Company"}, nil
	}
	if _, err := d.InstallLogic("pay", "Company", l, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		out Output
		rec *DispatchRecord
	}
	resCh := make(chan result, 1)
	go func() {
		out, rec := d.Invoke("o", "pay", Input{"amount": 1})
		resCh <- result{out, rec}
	}()
	<-block
	if err := d.RevokeObject("o"); err != nil {
		t.Fatal(err)
	}
	close(release)
	res := <-resCh
	if res.rec.Status != StatusPostconditionFailed {
		t.Fatalf("postcondition should see revoke, got %s: %s", res.rec.Status, res.rec.Err)
	}
}

// 前置/后置/执行错误都发生在分派成功之后。
func TestPrePostExecuteFailures(t *testing.T) {
	d, _ := mustSetup(t)
	l := rejectingLogic("L-Company")
	if _, err := d.InstallLogic("pay", "Company", l, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}
	_, rec := d.Invoke("o", "pay", Input{"amount": 1000})
	if rec.Status != StatusPreconditionFailed {
		t.Fatalf("got %s", rec.Status)
	}

	l2 := logicID("L-Company2")
	l2.Execute = func(_ *Object, _ Input) (Output, error) { return nil, errors.New("boom") }
	if _, err := d.InstallLogic("pay", "Company", l2, nil); err != nil {
		t.Fatal(err)
	}
	_, rec = d.Invoke("o", "pay", Input{"amount": 1})
	if rec.Status != StatusExecuteFailed {
		t.Fatalf("got %s", rec.Status)
	}

	l3 := logicID("L-Company3")
	l3.Post = func(_ *Object, _ Input, _ Output) error { return errors.New("post nope") }
	if _, err := d.InstallLogic("pay", "Company", l3, nil); err != nil {
		t.Fatal(err)
	}
	_, rec = d.Invoke("o", "pay", Input{"amount": 1})
	if rec.Status != StatusPostconditionFailed {
		t.Fatalf("got %s", rec.Status)
	}
}
