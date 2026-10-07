package hypchecktest

import (
	"testing"

	"ontology/hypcheck"
)

// TestHookBoundaryExhaustive 穷举钩子在切换时刻两侧的取值：
// 规定为左闭右开 [from, until)，即边界时刻 T 必须取新版本一侧。
func TestHookBoundaryExhaustive(t *testing.T) {
	for _, at := range []hypcheck.Timestamp{0, 1, 2, 9, 10, 11, 19, 20, 21} {
		e := hypcheck.NewEngine(hypcheck.Config{})
		tv := hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
			{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true},
		}}}
		must(t, e.DefineType(0, "A", tv))
		must(t, e.UpsertPrincipal(0, "u", true))
		must(t, e.ToggleGrant(0, "u", "A", true))
		// v1 在 t=10 前生效：参数 p=="x" 拒绝；t=10 切换为 v2：p=="y" 拒绝。
		must(t, e.PublishHook(0, "h", hypcheck.PhasePre, hypcheck.HookVersion{Version: "v1",
			Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.StrValue("x")}}))
		must(t, e.BindHook(0, "A", "h", hypcheck.PhasePre))
		must(t, e.PublishHook(10, "h", hypcheck.PhasePre, hypcheck.HookVersion{Version: "v2",
			Spec: hypcheck.HookSpec{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.StrValue("y")}}))

		wantVer := "v1"
		wantDenied := "x"
		if at >= 10 {
			wantVer, wantDenied = "v2", "y"
		}
		r := e.Precheck(hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u",
			Params: hypcheck.Params{"p": hypcheck.StrValue(wantDenied)}})
		if r.Verdict != hypcheck.VerdictDenied {
			t.Fatalf("at=%d: expected denied by %s, got %s %s", at, wantVer, r.Verdict, r.Message)
		}
		if len(r.Hooks) != 1 || r.Hooks[0].Version != wantVer {
			t.Fatalf("at=%d: expected hook %s, got %+v", at, wantVer, r.Hooks)
		}
		if len(r.PreFailures) == 0 {
			t.Fatalf("at=%d: expected pre failure", at)
		}
		// 另一侧取值在同一时刻必须被允许，证明边界方向唯一。
		other := "x"
		if wantDenied == "x" {
			other = "y"
		}
		r2 := e.Precheck(hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u",
			Params: hypcheck.Params{"p": hypcheck.StrValue(other)}})
		if r2.Verdict != hypcheck.VerdictAllowed {
			t.Fatalf("at=%d: complementary value should be allowed, got %s %s", at, r2.Verdict, r2.Message)
		}
	}
}

// TestPermissionBoundaryExhaustive 穷举继承边与授权在切换边界两侧的快照方向。
func TestPermissionBoundaryExhaustive(t *testing.T) {
	for _, at := range []hypcheck.Timestamp{4, 5, 6, 9, 10, 11} {
		e := hypcheck.NewEngine(hypcheck.Config{})
		must(t, e.DefineType(0, "A", hypcheck.TypeVersion{}))
		must(t, e.UpsertPrincipal(0, "u", true))
		must(t, e.UpsertPrincipal(0, "org", true))
		must(t, e.ToggleGrant(0, "org", "A", true))
		// u 在 t=10 才继承 org；边界 t=10 取新侧（已继承）。
		must(t, e.ToggleEdge(10, "org", "u", true))

		r := e.Precheck(hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u"})
		if at < 10 {
			if r.Verdict != hypcheck.VerdictDenied || len(r.PreFailures) == 0 {
				t.Fatalf("at=%d: expected permission denial, got %s", at, r.Verdict)
			}
			if r.PermTrace == nil || r.PermTrace.Allowed {
				t.Fatalf("at=%d: perm trace should be disallowed", at)
			}
		} else {
			if r.Verdict != hypcheck.VerdictAllowed {
				t.Fatalf("at=%d: expected allowed via inherited grant, got %s %s", at, r.Verdict, r.Message)
			}
			if r.PermTrace == nil || !r.PermTrace.Allowed || r.PermTrace.GrantedBy != "org" {
				t.Fatalf("at=%d: bad perm trace %+v", at, r.PermTrace)
			}
		}
	}

	// 边在 t=5 从激活切为失效：边界取新侧（已断开）。
	e := hypcheck.NewEngine(hypcheck.Config{})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{}))
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.UpsertPrincipal(0, "org", true))
	must(t, e.ToggleGrant(0, "org", "A", true))
	must(t, e.ToggleEdge(0, "org", "u", true))
	must(t, e.ToggleEdge(5, "org", "u", false))
	for _, at := range []hypcheck.Timestamp{4, 5} {
		r := e.Precheck(hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u"})
		if at < 5 && r.Verdict != hypcheck.VerdictAllowed {
			t.Fatalf("at=%d want allowed", at)
		}
		if at >= 5 && r.Verdict != hypcheck.VerdictDenied {
			t.Fatalf("at=%d want denied after edge removal", at)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
